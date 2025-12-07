package server

import (
	"github.com/ndsky1003/net/logger"
)

// service_manager 服务管理策略接口
type service_manager interface {

	// OnConnect 当新服务连接时调用
	OnConnect(Session) error

	// OnDisconnect 当服务断开时调用
	OnDisconnect(Session, error) error

	// OnMessage 当收到服务消息时调用
	//WARN: HandleMsg 处理接收到的消息。
	//  --------------
	// ⚠️ 警告 (Memory Safety Warning):
	// 传入的 data 切片底层引用了连接的共享读取缓冲区。
	// 该数据仅在 HandleMsg 函数调用期间有效。
	// ---------------
	// 1. 如果你是同步处理（如反序列化、解析），直接使用 data 即可，性能最高。
	// 2. 如果你需要异步处理（如 go func, 丢进 channel），或者需要长期持有该数据，
	//    必须先拷贝一份：dataCopy := append([]byte(nil), data...)
	OnMessage(s Session, data []byte) error

	// Close 清理资源。
	Close() error
}

type DefaultServiceManager struct {
}

func (this DefaultServiceManager) OnConnect(s Session) error {
	logger.Infof("Service %s connected", s.ID())
	return nil
}

func (this DefaultServiceManager) OnDisconnect(s Session, err error) error {
	logger.Infof("Service %s disconnected", s.ID())
	return nil
}

func (this DefaultServiceManager) OnMessage(s Session, data []byte) error {
	logger.Infof("Service %s OnMessage", sid)
	return nil
}

func (this DefaultServiceManager) Close() error {
	logger.Info("Service Close")
	return nil
}
