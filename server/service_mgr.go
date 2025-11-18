package server

import (
	"github.com/ndsky1003/net/conn"
)

// ServiceManager 服务管理策略接口
type ServiceManager interface {
	// OnConnect 当新服务连接时调用
	OnConnect(conn *conn.Conn, serviceID string) error

	// OnDisconnect 当服务断开时调用
	OnDisconnect(serviceID string)

	// OnMessage 当收到服务消息时调用
	OnMessage(serviceID string, data []byte) error

	// GetService 获取服务信息
	// GetService(serviceID string) (ServiceInfo, bool)

	// ListServices 列出所有服务
	// ListServices() []ServiceInfo

	// Broadcast 向所有服务广播消息
	Broadcast(data []byte) error

	// SendToService 向特定服务发送消息
	SendToService(serviceID string, data []byte) error
}
