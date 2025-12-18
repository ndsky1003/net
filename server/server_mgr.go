package server

import (
	"log/slog"
)

// service_manager 服务管理策略接口
type server_manager interface {

	// OnConnect 当新服务连接时调用
	OnConnect(Session) error

	// OnDisconnect 当服务断开时调用
	OnDisconnect(Session, error) error

	// 这里的data 生命周期由上游控制,比如池化技术,做到真正的zero_copy
	OnMessage(s Session, data []byte) error

	// Close 清理资源。
	Close() error
}

type DefaultServerManager struct {
}

func (this DefaultServerManager) OnConnect(s Session) error {
	slog.Info("DefaultServerManager OnConnect", "ID", s.ID())
	return nil
}

func (this DefaultServerManager) OnDisconnect(s Session, err error) error {
	slog.Info("DefaultServerManager OnDisconnect", "ID", s.ID(), "error", err)
	return nil
}

func (this DefaultServerManager) OnMessage(s Session, data []byte) error {
	slog.Info("DefaultServerManager OnMessage", "ID", s.ID(), "data_len", len(data))
	return nil
}

func (this DefaultServerManager) Close() error {
	slog.Info("DefaultServerManager Close")
	return nil
}
