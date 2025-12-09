package server

import (
	"context"

	"github.com/google/uuid"
	"github.com/ndsky1003/net/conn"
)

// Session 定义了服务端会话的操作接口
type Session interface {
	// ID 获取当前会话 ID
	ID() uuid.UUID

	// SetID 设置当前会话 ID,由上游控制,因为id有可能复用,有可能断线重连的时候,有可能需要设置相同id,而不是一致用最新分配的id
	SetID(newID uuid.UUID)

	// Conn 获取底层连接 (用于发送消息等)
	Conn() *conn.Conn

	// Send 语法糖 (可选，方便用户直接回消息)
	Send(ctx context.Context, data []byte, opts ...*Option) error

	// Send 语法糖 (可选，方便用户直接回消息)
	Sends(ctx context.Context, data [][]byte, opts ...*Option) error
}

// sessionImpl 是原本 handler_helper 的升级版
type default_Session struct {
	sid  uuid.UUID
	conn *conn.Conn
	mgr  server_manager
}

func (s *default_Session) ID() uuid.UUID {
	return s.sid
}

func (s *default_Session) SetID(newID uuid.UUID) {
	s.sid = newID
}

func (s *default_Session) Conn() *conn.Conn {
	return s.conn
}

func (s *default_Session) Send(ctx context.Context, data []byte, opts ...*Option) error {
	opt := Options().Merge(opts...)
	return s.conn.Send(ctx, data, &opt.Option)
}

func (s *default_Session) Sends(ctx context.Context, data [][]byte, opts ...*Option) error {
	opt := Options().Merge(opts...)
	return s.conn.Sends(ctx, data, &opt.Option)
}

// HandleMsg 实现 conn.Handler 接口
func (s *default_Session) HandleMsg(data []byte) error {
	// 直接把自己传给管理器，因为自己就是 Session
	return s.mgr.OnMessage(s, data)
}
