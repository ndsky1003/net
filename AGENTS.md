# AGENTS.md

纯 Go 库：一个职责单一的 TCP 协议层（组帧/心跳/读写泵），不涉及业务。对外只暴露 `Conn` 类型（根包 `net`）。

## 结构
- 源码集中在根目录两个文件：`conn.go`（`Conn`、读写泵、心跳）、`option.go`（`Option` 配置）。
- 测试在 `conn_test.go`；模块 `github.com/ndsky1003/net`，Go 1.23，无第三方依赖。

## 命令
- 构建/测试：`go build ./...`、`go test ./...`（可用 `-race`）。
- 基准：`go test -bench=. -benchmem -run='^$' .`（无 CI、无 Makefile、无 lint 配置）。

## 关键约定（容易踩坑）
- **zero-copy**：帧 body 直接引用调用方 `[]byte`，不复制。`Write` 返回只代表"已入队"，调用方在 writev 完成前不能复用/归还该 data。
- `WriteReader(r, size)`：从 `io.Reader` 流式读 `size` 字节作为一帧，发送端按 `ReadBlockSize` 分块读+写（不攒批、复制而非 zero-copy），接收端仍分块回调；返回只代表"已入队"，`r` 在整帧写完前不能复用/关闭。
- `Handler.HandleMsg(remain int, body []byte)`：大 body 会被按 `ReadBlockSize`(默认 64KB) 分块多次回调，`remain` 是本帧剩余未交付字节数，`0` 表示本帧结束。
- `Conn` 有三个方法只能在 `Serve()` 之前用（握手阶段）：`WriteToConn`、`Read`、`SetHandler`。
- 心跳/`pong` 必须走 `TryWrite`（非阻塞）：它们本身在 read/write pump 里执行，用阻塞 `Write` 会自己等自己死锁。
- 主动停连接调 `Conn.Close()`（幂等）；cancel 传入的 ctx 会让 `writePump` 退出并触发关闭。
- `MsgChanSize` 默认 512：小帧（单块）下读入速度超过 Handler 消费速度时 `msgChan` 满会断连（"msgChan is full"）；大帧分块是阻塞背压、不断连。
- 帧格式：`[magic 0xAA 1B][flag 1B][bodyLen 4B BigEndian][body]`，单帧上限默认 64KB（`MaxFrameSize` 可调大，发大包时收发两端都要调）。
