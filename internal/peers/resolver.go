package peers

import (
	"context"

	"github.com/gotd/td/tg"
)

// AccessHashProvider 定义一个能返回带有效 access_hash 的 InputPeer 的提供者。
type AccessHashProvider interface {
	GetInputPeer(ctx context.Context, peerID int64) (tg.InputPeerClass, error)
	GetUserPeerWithFallback(ctx context.Context, userID int64, channelPeer tg.InputChannelClass) (*tg.InputPeerUser, error)
	GetUserPeerFromMessage(ctx context.Context, peer tg.InputPeerClass, msgID int, userID int64) (*tg.InputPeerUser, error)
}

// Resolver 现在作为轻量转换器，仅委托 AccessHashProvider 获取带有效 access_hash 的 InputPeer。
type Resolver struct {
	provider AccessHashProvider
}

func NewResolver(provider AccessHashProvider) *Resolver {
	return &Resolver{provider: provider}
}

// ResolveFromChatID 根据 chatID 返回可用的 InputPeer。
// 规则：chatID>0 用户；-x 普通群；-100... 为频道/超级群。
func (r *Resolver) ResolveFromChatID(ctx context.Context, chatID int64) (tg.InputPeerClass, error) {
	return r.provider.GetInputPeer(ctx, chatID)
}

// ResolveUserInChannel 在指定频道/超级群上下文中解析用户，带回退策略。
func (r *Resolver) ResolveUserInChannel(ctx context.Context, channelPeer tg.InputChannelClass, userID int64) (tg.InputPeerClass, error) {
	userPeer, err := r.provider.GetUserPeerWithFallback(ctx, userID, channelPeer)
	if err != nil {
		return nil, err
	}
	return userPeer, nil
}

// ResolveUserFromMessage 优先通过回复消息上下文解析用户 access_hash。
func (r *Resolver) ResolveUserFromMessage(ctx context.Context, peer tg.InputPeerClass, msgID int, userID int64) (tg.InputPeerClass, error) {
	userPeer, err := r.provider.GetUserPeerFromMessage(ctx, peer, msgID, userID)
	if err != nil {
		return nil, err
	}
	return userPeer, nil
}

// 兼容旧调用：用户和频道细粒度解析已由 AccessHashManager 承担。
