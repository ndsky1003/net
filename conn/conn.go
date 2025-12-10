package conn

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ndsky1003/net/logger"
)

const (
	flag_msg byte = 1 << iota
	flag_ping
	flag_pong
)

type Handler interface {
	//WARN: HandleMsg 处理接收到的消息。
	//--------------
	// ⚠️ 警告 (MEMORY UNSAFE):
	// 传入的 data 切片直接引用连接内部的共享缓冲区 (readBuf)。
	// 该数据仅在 HandleMsg 函数同步执行期间有效！
	//--------------
	// ✅ 安全做法 (同步处理):
	//    1. 直接解析: json.Unmarshal(data, &obj)
	//    2. 路由分发: router.Dispatch(data)
	//--------------
	// ❌ 危险做法 (异步/持有):
	//    1. go func() { process(data) } // data 会被后续网络包覆盖，变脏数据
	//    2. msgChan <- data             // 同上
	//    3. globalCache = data          // 同上
	//--------------
	// 💡 如需异步处理，必须手动拷贝:
	//    clone := make([]byte, len(data))
	//    copy(clone, data)
	//    go process(clone)
	HandleMsg(data []byte) error
}

type Conn struct {
	net.Conn
	r        io.Reader
	w        *bufio.Writer
	sendChan chan *msg
	handler  Handler
	opt      *Option

	closed atomic.Bool // 原子状态标记
	ctx    context.Context
	cancel context.CancelFunc

	// readBuf 用于复用读取内存，避免反复 make ,避免gc的碎片化问题
	readBuf     []byte
	shrinkCount int

	l        sync.Mutex // protect closeErr
	closeErr error
}

func New(ctx context.Context, conn net.Conn, handler Handler, opts ...*Option) *Conn {
	opt := Options().
		SetTimeout(10 * time.Second).
		SetReadTimeoutFactor(2.2).
		SetHeartInterval(5 * time.Second).
		SetSendChanSize(100).
		SetReadBufferLimitSize(100 * 1024 * 1024). //100M
		SetReadBufferMaxSize(64 * 1024).           //64k
		SetReadBufferMinSize(4 * 1024).            //4k
		SetShrinkThreshold(50).                    //50
		Merge(opts...)
	ctx, cancel := context.WithCancel(ctx)
	c := &Conn{
		Conn:     conn,
		r:        bufio.NewReader(conn),
		w:        bufio.NewWriter(conn),
		handler:  handler,
		opt:      &opt,
		ctx:      ctx,
		cancel:   cancel,
		sendChan: make(chan *msg, *opt.SendChanSize),
		readBuf:  make([]byte, 0, *opt.ReadBufferMinSize),
	}
	c.closed.Store(false)
	return c
}

// WARNING: 非线程安全，由 writePump 独占调用
func (this *Conn) write(flag byte, data []byte, opts ...*Option) (err error) {
	return this.writes(flag, [][]byte{data}, opts...)
}

func (this *Conn) writes(flag byte, datas [][]byte, opts ...*Option) (err error) {
	opt := this.opt.Merge(opts...)
	length := 0
	for _, data := range datas {
		length += len(data)
	}
	var size [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(size[:], uint64(length)+1)

	var deadline time.Time
	if t := opt.WriteTimeout; t != nil {
		if *t != 0 {
			deadline = time.Now().Add(*t)
		}
	}

	if err = this.Conn.SetWriteDeadline(deadline); err != nil {
		return
	}

	if _, err = this.w.Write(size[:n]); err != nil {
		return
	}

	if err = this.w.WriteByte(flag); err != nil {
		return
	}

	for _, data := range datas {
		if _, err = this.w.Write(data); err != nil {
			return
		}
	}
	return
}

// 计算下一个梯子容量（2的幂次方，且 >= n）
func nextPowerOf2(n int, minCap int) int {
	if n <= minCap {
		return minCap
	}
	cap := minCap
	for cap < n {
		cap <<= 1 // 乘以 2：4K -> 8K -> 16K -> 32K ...
	}
	return cap
}

// WARNING: 非线程安全，由 readPump 独占调用
func (this *Conn) read(opts ...*Option) (flag byte, data []byte, err error) {
	opt := this.opt.Merge(opts...)
	read_buf_limit_size := *opt.ReadBufferLimitSize

	var deadline time.Time
	if t := opt.ReadTimeout; t != nil {
		if *t != 0 {
			deadline = time.Now().Add(*t)
		}
	}
	if err = this.Conn.SetReadDeadline(deadline); err != nil {
		return
	}

	size, err := binary.ReadUvarint(this.r.(io.ByteReader))
	if err != nil {
		return
	}

	if size > read_buf_limit_size {
		err = fmt.Errorf("frame size %d exceeds maximum %d", size, read_buf_limit_size)
		return
	}

	if size == 0 {
		err = fmt.Errorf("invalid frame size 0")
		return
	}

	// ================== 梯子型内存管理 START ==================

	currentCap := cap(this.readBuf)
	minCap := *opt.ReadBufferMinSize
	maxCap := *opt.ReadBufferMaxSize
	shrinkThreshold := *opt.ShrinkThreshold

	// 1. 判断是否属于突发超大流量 (> 64KB)
	// 这种包不走梯子，直接临时分配，用完即毁，不污染 readBuf
	if size > uint64(maxCap) {
		// 临时分配，不修改 this.readBuf
		tempBuf := make([]byte, size)
		if _, err = io.ReadFull(this.r, tempBuf); err != nil {
			return
		}
		flag = tempBuf[0]
		data = tempBuf[1:]
		return
	}

	// 计算当前 size 所需的“目标台阶”
	targetCap := nextPowerOf2(int(size), *opt.ReadBufferMinSize) // 例如 size=5000 -> targetCap=8192

	if currentCap >= targetCap {
		// --- 情况 A：容量够用 ---

		// 尝试触发【缩容逻辑】（下梯子）
		// 只有当：
		// 1. 当前容量比目标容量大很多（例如当前 64K，实际只需要 4K）
		// 2. 且 连续 N 次都只需要这么小
		// 我们才进行缩容。
		// 这里判定标准是：currentCap > targetCap * 2 (即利用率低于 50% 甚至更低时考虑)
		if currentCap > targetCap && currentCap > minCap {
			// 如果当前容量是目标容量的 4 倍以上（利用率 < 25%），我们记一次数
			if currentCap >= targetCap*4 {
				this.shrinkCount++

				// 只有连续 50 次都这么小，才真的缩容
				if this.shrinkCount > shrinkThreshold {
					// 缩容动作：容量减半（温和缩容），或者直接缩到 targetCap
					// 这里建议直接缩到 targetCap，或者 targetCap * 2 留点余地
					// 工业界通常做法：新建一个小的，把原来的丢给 GC
					newCap := targetCap * 2 // 留一点余量防止马上又反弹
					if newCap < currentCap {
						this.readBuf = make([]byte, newCap)
					}
					this.shrinkCount = 0 // 重置计数
				}
			} else {
				// 利用率还行，或者偶尔大包，重置计数器
				this.shrinkCount = 0
			}
		} else {
			// 容量合适，重置缩容计数
			this.shrinkCount = 0
		}

		// 复用内存
		this.readBuf = this.readBuf[:size]

	} else {
		// --- 情况 B：容量不够，需要扩容（上梯子）---

		// 直接扩容到目标台阶，而不是只扩容到 size
		// 例如：当前 4K，来了 5K 的包 -> 直接扩容到 8K
		this.readBuf = make([]byte, targetCap)
		this.readBuf = this.readBuf[:size]

		// 扩容后，清空缩容计数
		this.shrinkCount = 0
	}

	// ================== 梯子型内存管理 END ==================

	// 读取数据
	if _, err = io.ReadFull(this.r, this.readBuf); err != nil {
		return
	}

	flag = this.readBuf[0]
	data = this.readBuf[1:]
	return
}

func (this *Conn) ping() (err error) {

	if err = this.write(flag_ping, []byte{}); err != nil {
		err = fmt.Errorf("ping:%w", err)
		return
	}

	if err = this.Flush(); err != nil {
		return err
	}

	return
}

func (this *Conn) pong() error {
	msg := msgPool.Get().(*msg)
	msg.flag = flag_pong
	select {
	case this.sendChan <- msg:
	default:
		msg.Release()
		// 如果发送缓冲区满，丢弃 PONG 是安全的，对方会在下一个周期重试 PING
		// 或者对方发送业务数据时也会刷新活跃状态
		return fmt.Errorf("send pong buffer full")
	}
	return nil
}

// writePump 负责将 sendChan 中的数据写入连接，并维护心跳
// 采用“智能心跳”策略：仅在连接空闲时发送 PING
func (this *Conn) writePump() (err error) {
	heartInterval := *this.opt.HeartInterval
	// 发送检测周期设为心跳间隔的一半，确保有足够的冗余
	// keepAliveDuration := heartInterval / 2

	// 使用 Timer 实现弹性心跳
	timer := time.NewTimer(heartInterval)
	defer timer.Stop()

	for {
		select {
		case <-this.ctx.Done():
			return nil
		case msg, ok := <-this.sendChan:
			if !ok {
				return fmt.Errorf("sendChan closed")
			}

			// 发送数据（业务消息或 PONG）
			if err = this.writes(msg.flag, msg.data, msg.opt); err != nil {
				msg.Release()
				return err
			}
			msg.Release()
			// 2. 【关键策略】：检查通道里是否还有排队的数据？
			// 如果还有，就继续循环去拿，暂不 Flush，为了拼成大包。
			// 如果没有了，说明这波突发流量结束了，立刻 Flush 保证低延迟。
			if len(this.sendChan) > 0 {
				continue
			}

			if err = this.w.Flush(); err != nil {
				return
			}

			// 发送成功，连接处于活跃状态，重置 PING 定时器
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(heartInterval)

		case <-timer.C:
			// 定时器触发，说明 keepAliveDuration 时间内未发送任何数据
			// 发送 PING 维持连接活跃
			if err = this.ping(); err != nil {
				return err
			}
			// 发送 PING 后重置定时器
			timer.Reset(heartInterval)
		}
	}
}

// readPump 负责从连接读取数据，并作为“看门狗”检测连接超时
func (this *Conn) readPump() error {
	heartInterval := *this.opt.HeartInterval
	// 【修改点】优化超时策略
	// 发送间隔是  heartInterval。
	// 将超时设为 2.2 * heartInterval (或者 heartInterval + 2*time.Second)。
	// 意义：允许丢失 1 个心跳包 (1)，并允许第 2 个心跳包 (2) 晚到 20% 的时间。
	// 这比 2.0 倍敏感得多，能更快发现断连，同时防止轻微抖动导致的误断。
	readTimeout := time.Duration(float64(heartInterval) * *this.opt.ReadTimeoutFactor)
	for {
		// 每次读取前设置 DeadLine，给连接“续命”
		flag, body, err := this.read(Options().SetReadTimeout(readTimeout))
		if err != nil {
			// 如果超时，这里会返回 i/o timeout 错误
			return fmt.Errorf("read error: %w", err)
		}

		// 收到任何数据，说明连接是健康的。下一次循环会重新设置 ReadDeadline。

		switch flag {
		case flag_msg:
			if this.handler != nil {
				func() {
					defer func() {
						if r := recover(); r != nil {
							logger.Infof("handler panic: %v", r)
						}
					}()
					if err := this.handler.HandleMsg(body); err != nil {
						logger.Infof("handle msg error: %v", err)
					}
				}()
			}
		case flag_ping:
			// 收到 PING，回复 PONG
			if err := this.pong(); err != nil {
				return err
			}
		case flag_pong:
			// 收到 PONG，仅表示对方活着，ReadDeadline 已自动刷新，无需操作
			// log.Println("receive pong")
		default:
			return fmt.Errorf("unknown flag: %d", flag)
		}
	}
}
