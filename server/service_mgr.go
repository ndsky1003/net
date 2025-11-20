package server

import (
	"log"

	"github.com/ndsky1003/net/conn"
)

// service_manager 服务管理策略接口
type service_manager interface {

	// OnConnect 当新服务连接时调用
	OnConnect(sid string, conn *conn.Conn) error

	// OnDisconnect 当服务断开时调用
	OnDisconnect(serviceID string, err error) error

	// OnMessage 当收到服务消息时调用
	OnMessage(sid string, data []byte) error

	//清理资源
	Close() error
}

type DefaultServiceManager struct {
}

func (this DefaultServiceManager) OnConnect(sid string, conn *conn.Conn) error {
	log.Printf("Service %s connected", sid)
	return nil
}

func (this DefaultServiceManager) OnDisconnect(sid string, err error) error {
	log.Printf("Service %s disconnected", sid)
	return nil
}

func (this DefaultServiceManager) OnMessage(sid string, data []byte) error {
	log.Printf("Service %s OnMessage", sid)
	return nil
}

func (this DefaultServiceManager) Close() error {
	log.Println("Service Close")
	return nil
}
