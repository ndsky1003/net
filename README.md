# net

纯 Go 的 TCP 协议层库：只负责组帧、心跳和读写泵，不涉及任何业务。对外只暴露 `Conn` 类型，连接本身由调用方建立（`net.Dial` / `net.Listen`）。

## 特性

- **zero-copy**：帧 body 直接引用调用方的 `[]byte`，收发全程不复制。
- **组帧/拆帧**：6 字节定长帧头，单帧 body 上限可配（默认 64KB）。
- **流式大包**：大 body 按块读取、逐块回调，`remain` 指示剩余，不一次性读入内存。
- **心跳**：PING/PONG 看门狗，空闲才发，不阻塞读写。
- **批量写**：多帧攒成一批，用 `net.Buffers` 一次 `writev` 发出。
- **有序分发**：单连接内的业务帧严格按收包顺序串行交给 `Handler`。

## 安装

```
go get github.com/ndsky1003/net
```

## 快速开始

```go
package main

import (
	"context"
	"net"

	protocol "github.com/ndsky1003/net"
)

type handler struct{}

func (handler) HandleMsg(remain int, body []byte) error {
	// 处理一块业务数据；remain 是本帧剩余字节数，0 表示本帧结束
	return nil
}

func serve(conn net.Conn) {
	c := protocol.New(context.Background(), conn, handler{})
	// Serve 阻塞到连接终止；对端断连/超时返回错误，主动关闭返回 ErrConnClosed
	c.Serve()
}

func main() {
	// 服务端
	ln, _ := net.Listen("tcp", ":8080")
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go serve(conn)
	}

	// 客户端
	conn, _ := net.Dial("tcp", "localhost:8080")
	c := protocol.New(context.Background(), conn, handler{})
	go c.Serve()

	c.Write([]byte("hello")) // 返回只代表"已入队"
}
```

## API

| 符号 | 说明 |
| --- | --- |
| `New(ctx, conn, handler, opts...)` | 包装一条已建立的连接，返回 `*Conn` |
| `Serve()` | 启动读写泵并阻塞到连接终止，返回终止原因 |
| `Write(data)` | 组帧入队（zero-copy），阻塞直到入队、ctx 结束或连接关闭 |
| `WriteReader(r, size)` | 从 `io.Reader` 流式读 `size` 字节作为一帧，发送端分块读入省内存 |
| `Close()` / `CloseWithError(err)` | 主动关闭，幂等；`Err()` 返回关闭原因 |
| `Handler` | 业务帧处理器接口，`HandleMsg(remain int, body []byte) error` |
| `ErrConnClosed` | 主动关闭时的标准错误 |

## 帧格式

```
[magic 0xAA 1B][flag 1B][bodyLen 4B BigEndian][body]
```

| flag | 含义 |
| --- | --- |
| `1` | 业务消息，body 交给 `Handler` |
| `2` | PING |
| `4` | PONG |

单帧 body 上限默认 64KB，可用 `Option.SetMaxFrameSize` 调整。

## Option

```go
protocol.Options().
	SetHeartInterval(5 * time.Second).
	SetMaxFrameSize(64 << 10)
```

| 选项 | 默认值 | 说明 |
| --- | --- | --- |
| `HeartInterval` | 5s | 看门狗周期，空闲超过它才发 PING |
| `ReadTimeoutFactor` | 2.2 | 判死时限 = `HeartInterval` × 系数 |
| `WriteTimeoutFactor` | 2.2 | 单次 `writev` 上限 = `HeartInterval` × 系数 |
| `MaxFrameSize` | 64KB | 单帧 body 上限 |
| `MaxBatchFrames` | 512 | 一次 `writev` 最多攒多少帧 |
| `ReadBlockSize` | 64KB | 接收端把大 body 分块读入的粒度 |
| `SendChanSize` | 30 | 发送队列缓冲 |
| `MsgChanSize` | 512 | 接收队列缓冲（不低于 `MaxBatchFrames`） |

## 大文件传输

单帧 body 上限默认 64KB，可调大（`SetMaxFrameSize`）以承载大包。接收端不会把整个 body 一次性读入内存，而是按 64KB 分块、逐块回调 `Handler`：`remain` 是本帧剩余未交付字节数，`remain == 0` 表示本帧交付完毕。

发送端有两种方式，二选一：

```go
// 1) 数据已在内存：zero-copy，直接发（零复制，但需持有整个 data）
c.Write(file) // file 是 1G 数据

// 2) 数据在磁盘/流：WriteReader 分块读入，不必一次持有 1G 内存
f, _ := os.Open("big.file")
c.WriteReader(f, fileSize)
```

```go
// 接收端：逐块累积（写文件），remain == 0 时一个完整包收完
func (h *handler) HandleMsg(remain int, body []byte) error {
    h.file.Write(body)
    if remain == 0 {
        h.file.Close()
    }
    return nil
}
```

注意：发送端与接收端都要调大 `MaxFrameSize`，否则超长帧会被拒绝；`WriteReader` 会复制（放弃 zero-copy），换取发送端内存可控。

## 注意事项

- **zero-copy**：`Write` 返回只代表"已入队"，data 在 `writev` 完成前不能复用或归还。
- `WriteToConn` / `Read` / `SetHandler` 只能在 `Serve()` 之前调用（握手阶段）。
- 主动停连接用 `Close()`（幂等）；cancel 传入的 ctx 也会让 `writePump` 退出并触发关闭。
- `MsgChanSize` 默认 512：小帧下入队速度超过 `Handler` 消费速度时 `msgChan` 满会断连；大帧分块是阻塞背压、不断连。
