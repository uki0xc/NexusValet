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
		// 对于用户（包括机器人），尝试获取正确的 AccessHash
		return r.ResolveUserByID(ctx, chatID)
	}
	if chatID > -1000000000000 {
		return &tg.InputPeerChat{ChatID: -chatID}, nil
	}
	channelID := -chatID - 1000000000000
	return r.ResolveChannelByID(ctx, channelID)
}

// ResolveUserByID 解析用户ID，尝试获取正确的AccessHash
func (r *Resolver) ResolveUserByID(ctx context.Context, userID int64) (tg.InputPeerClass, error) {
	// 方法1：尝试通过 UsersGetUsers 获取用户信息
	users, err := r.api.UsersGetUsers(ctx, []tg.InputUserClass{
		&tg.InputUser{UserID: userID, AccessHash: 0},
	})
	if err == nil && len(users) > 0 {
		if user, ok := users[0].(*tg.User); ok {
			return &tg.InputPeerUser{UserID: user.ID, AccessHash: user.AccessHash}, nil
		}
	}

	// 方法2：从对话中查找用户
	dialogs, err := r.api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
		OffsetDate: 0,
		OffsetID:   0,
		OffsetPeer: &tg.InputPeerEmpty{},
		Limit:      100,
	})
	if err == nil {
		peer := r.searchUserInDialogs(dialogs, userID)
		if peer != nil {
			return peer, nil
		}
	}

	// 方法3：最后的回退 - 使用 AccessHash=0
	// 对于一些情况，Telegram 服务器可能会接受 AccessHash=0
	return &tg.InputPeerUser{UserID: userID, AccessHash: 0}, nil
}

// searchUserInDialogs 在对话列表中搜索指定的用户
func (r *Resolver) searchUserInDialogs(dialogs tg.MessagesDialogsClass, userID int64) tg.InputPeerClass {
	var users []tg.UserClass

	if ds, ok := dialogs.(*tg.MessagesDialogs); ok {
		users = ds.Users
	} else if ds, ok := dialogs.(*tg.MessagesDialogsSlice); ok {
		users = ds.Users
	}

	for _, u := range users {
		if user, ok := u.(*tg.User); ok && user.ID == userID {
			return &tg.InputPeerUser{UserID: user.ID, AccessHash: user.AccessHash}
		}
	}
	return nil
}

// ResolveChannelByID 解析频道/超级群组的 AccessHash 并返回 InputPeerChannel。
func (r *Resolver) ResolveChannelByID(ctx context.Context, channelID int64) (tg.InputPeerClass, error) {
	// 方法1：优先尝试使用 ChannelsGetChannels（AccessHash=0 由服务器返回细节）
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

	// 方法2：从最近对话中查找缓存
	dialogs, derr := r.api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
		OffsetDate: 0,
		OffsetID:   0,
		OffsetPeer: &tg.InputPeerEmpty{},
		Limit:      100,
	})
	if derr == nil {
		peer := r.searchChannelInDialogs(dialogs, channelID)
		if peer != nil {
			return peer, nil
		}
	}

	// 方法3：尝试获取更多对话
	if derr == nil {
		dialogs2, err2 := r.api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
			OffsetDate: 0,
			OffsetID:   0,
			OffsetPeer: &tg.InputPeerEmpty{},
			Limit:      500, // 获取更多对话
		})
		if err2 == nil {
			peer := r.searchChannelInDialogs(dialogs2, channelID)
			if peer != nil {
				return peer, nil
			}
		}
	}

	// 方法4：尝试使用 ContactsResolveUsername (如果有用户名的话)
	// 这里暂时跳过，因为我们没有用户名信息

	return nil, fmt.Errorf("channel not found: %d (tried multiple resolution methods, original errors: channels=%v, dialogs=%v)", channelID, err, derr)
}

// searchChannelInDialogs 在对话列表中搜索指定的频道
func (r *Resolver) searchChannelInDialogs(dialogs tg.MessagesDialogsClass, channelID int64) tg.InputPeerClass {
	if ds, ok := dialogs.(*tg.MessagesDialogs); ok {
		for _, chat := range ds.Chats {
			if ch, ok := chat.(*tg.Channel); ok && ch.ID == channelID {
				return &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}
			}
		}
	} else if ds, ok := dialogs.(*tg.MessagesDialogsSlice); ok {
		for _, chat := range ds.Chats {
			if ch, ok := chat.(*tg.Channel); ok && ch.ID == channelID {
				return &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}
			}
		}
	}
	return nil
}
