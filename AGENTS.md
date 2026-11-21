# AGENTS.md

纯 Go 库：一个职责单一的 TCP 协议层（组帧/心跳/读写泵），不涉及业务。对外只暴露 `Conn` 类型（根包 `net`）。

## 当前状态（WIP 重构，重要）
- 工作区处于 **v2 重构中**：旧的 `api.go`、`client/`、`server/`、`conn/` 已从工作区删除（仍在 HEAD 提交里），新逻辑收敛到根目录单个 `conn.go`（未跟踪）。
- **`go build ./...` 当前会失败**：`conn.go` 引用了未定义的 `ErrConnClosed`（196/205/322 行），需要补定义才能编译通过。旧 `conn` 子包里的等价量叫 `ErrConnectionClosed`。
- `go.mod` 里 `github.com/google/uuid` 已无任何引用，是旧代码遗留，可删。

## 命令
- 构建/测试：`go build ./...`、`go test ./...`（无 CI、无 Makefile、无 lint 配置）。
- 无任何测试文件；模块 `github.com/ndsky1003/net/v2`，Go 1.23。

## 关键约定（容易踩坑）
- **zero-copy**：帧 body 直接引用调用方 `[]byte`，不复制。`Write` 返回只代表"已入队"，调用方在 writev 完成前不能复用/归还该 data。
- `Conn` 有三个方法只能在 `Serve()` 之前用（握手阶段）：`WriteToConn`、`Read`、`SetHandler`。
- 心跳/`pong` 必须走 `TryWrite`（非阻塞）：它们本身在 read/write pump 里执行，用阻塞 `Write` 会自己等自己死锁。
- 主动停连接调 `Conn.Close()`（幂等）；cancel 传入的 ctx 会让 `writePump` 退出并触发关闭。
- 帧格式：`[magic 0xAA 1B][flag 1B][bodyLen 4B BigEndian][body]`，单帧上限 64KB（`conn.go` 顶部常量）。
