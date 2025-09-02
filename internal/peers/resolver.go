package peers

import (
	"context"
	"fmt"

	"github.com/gotd/td/tg"
)

// Resolver 封装使用 gotd 自动解析 peer 与 AccessHash 的逻辑。
type Resolver struct {
	api *tg.Client
}

func NewResolver(api *tg.Client) *Resolver {
	return &Resolver{api: api}
}

// ResolveFromChatID 根据 chatID 返回可用的 InputPeer。
// 规则：chatID>0 用户；-x 普通群；-100... 为频道/超级群。
func (r *Resolver) ResolveFromChatID(ctx context.Context, chatID int64) (tg.InputPeerClass, error) {
	if chatID > 0 {
		return &tg.InputPeerUser{UserID: chatID}, nil
	}
	if chatID > -1000000000000 {
		return &tg.InputPeerChat{ChatID: -chatID}, nil
	}
	channelID := -chatID - 1000000000000
	return r.ResolveChannelByID(ctx, channelID)
}

// ResolveChannelByID 解析频道/超级群组的 AccessHash 并返回 InputPeerChannel。
func (r *Resolver) ResolveChannelByID(ctx context.Context, channelID int64) (tg.InputPeerClass, error) {
	// 优先尝试使用 ChannelsGetChannels（AccessHash=0 由服务器返回细节）
	channels, err := r.api.ChannelsGetChannels(ctx, []tg.InputChannelClass{
		&tg.InputChannel{ChannelID: channelID, AccessHash: 0},
	})
	if err == nil {
		if chats, ok := channels.(*tg.MessagesChats); ok {
			for _, c := range chats.Chats {
				if ch, ok := c.(*tg.Channel); ok && ch.ID == channelID {
					return &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}, nil
				}
			}
		}
	}

	// 回退：从最近对话中查找缓存
	dialogs, derr := r.api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
		OffsetDate: 0,
		OffsetID:   0,
		OffsetPeer: &tg.InputPeerEmpty{},
		Limit:      100,
	})
	if derr != nil {
		return nil, fmt.Errorf("failed to get dialogs: %w (and channels err: %v)", derr, err)
	}
	if ds, ok := dialogs.(*tg.MessagesDialogs); ok {
		for _, chat := range ds.Chats {
			if ch, ok := chat.(*tg.Channel); ok && ch.ID == channelID {
				return &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}, nil
			}
		}
	} else if ds, ok := dialogs.(*tg.MessagesDialogsSlice); ok {
		for _, chat := range ds.Chats {
			if ch, ok := chat.(*tg.Channel); ok && ch.ID == channelID {
				return &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}, nil
			}
		}
	}
	return nil, fmt.Errorf("channel not found: %d", channelID)
}
