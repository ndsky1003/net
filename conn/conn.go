package conn

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"sync/atomic"
	"time"
)

const (
	flag_msg byte = 1 << iota
	flag_ping
	flag_pong
)

type Handler interface {
	HandleMsg(data []byte) error
}

type msg struct {
	flag byte
	data []byte
	opt  *Option
}

type Conn struct {
	net.Conn
	r              io.Reader
	w              *bufio.Writer
	sendChan       chan *msg
	last_msg_nano  atomic.Int64 //最新业务消息时间
	last_pong_nano atomic.Int64 //最新心跳消息时间
	handler        Handler
	opt            *Option

	closed atomic.Bool // 原子状态标记
	done   chan struct{}
}

func New(conn net.Conn, handler Handler, opts ...*Option) *Conn {
	opt := Options().
		SetWriteDeadline(10 * time.Second).
		SetHeartInterval(30 * time.Second).
		SetSendChanSize(100).
		SetMaxFrameSize(64 * 1024).
		Merge(opts...)
	c := &Conn{
		Conn:     conn,
		r:        bufio.NewReader(conn),
		w:        bufio.NewWriter(conn),
		handler:  handler,
		opt:      opt,
		done:     make(chan struct{}),
		sendChan: make(chan *msg, *opt.SendChanSize),
	}
	c.closed.Store(false)
	now := time.Now().Add(500 * time.Millisecond).UnixNano()
	// c.last_msg_nano.Store(now)
	c.last_pong_nano.Store(now)
	return c
}

// WARNING: 这个函数是非线程安全的，需要外部调用者保证
func (this *Conn) write(flag byte, data []byte, opts ...*Option) (err error) {
	opt := Options().Merge(this.opt).Merge(opts...)
	length := len(data)
	var size [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(size[:], uint64(length)+1)
	if err = this.Conn.SetWriteDeadline(time.Now().Add(*opt.WriteDeadline)); err != nil {
		return
	}
	if _, err = this.w.Write(size[:n]); err != nil {
		return
	}

	if err = this.w.WriteByte(flag); err != nil {
		return
	}

	if _, err = this.w.Write(data); err != nil {
		return
	}
	return this.w.Flush()
}

// - 采纳方案一：修正 readPump 的超时处理，让它成为主要的连接健康检测者？
// - 采納方案二：简化 readPump，让 writePump 成为唯一的连接健康检测者？
// 采用了方案二
// WARNING: 非线程安全
func (this *Conn) read(opts ...*Option) (flag byte, data []byte, err error) {
	opt := Options().Merge(this.opt).Merge(opts...)
	max_frame_size := *opt.MaxFrameSize
	if read_deadline := opt.ReadDeadline; read_deadline != nil && *read_deadline > 0 {
		this.Conn.SetReadDeadline(time.Now().Add(*read_deadline))
		defer this.Conn.SetReadDeadline(time.Time{}) // 恢复现场
	}
	size, err := binary.ReadUvarint(this.r.(io.ByteReader))
	if err != nil {
		return
	}
	if size > max_frame_size {
		err = fmt.Errorf("frame size %d exceeds maximum %d", size, max_frame_size)
		return
	}
	if size == 0 {
		err = fmt.Errorf("invalid frame size 0")
		return
	}
	raw_data := make([]byte, size)
	if _, err = io.ReadFull(this.r, raw_data); err != nil {
		return
	}
	flag = raw_data[0]
	data = raw_data[1:]
	return
}

func (this *Conn) ping() error {
	if err := this.write(flag_ping, []byte{}); err != nil {
		return fmt.Errorf("ping:%w", err)
	}
	return nil
}

// NOTE: 这里只能往队列里面放，如果直接写会有多线程问题，read和write是2个不同的goroutine，会引发var ErrShortWrite = errors.New("short write")
func (this *Conn) pong() error {
	select {
	case this.sendChan <- &msg{ //如果 Close() 先执行：sendChan 被关闭，select 会检测到通道关闭并panic
		flag: flag_pong,
		data: nil,
	}:
	default:
		return fmt.Errorf("send pong buffer full")
	}
	return nil
}

func (this *Conn) writePump() (err error) {
	heart_interval := *this.opt.HeartInterval
	ticker := time.NewTicker(heart_interval / 2) //探测周期缩短一半,提高探测精度
	defer ticker.Stop()
	for {
		select {
		case <-this.done:
			return
		case msg, ok := <-this.sendChan:
			if !ok {
				err = fmt.Errorf("sendChan has exhaust")
				return
			}
			if this.closed.Load() { // 额外检查
				return fmt.Errorf("connection closed ")
			}
			if err = this.write(msg.flag, msg.data, msg.opt); err != nil {
				return
			}
		case <-ticker.C:
			if this.closed.Load() { // 额外检查
				return fmt.Errorf("connection closed ")
			}
			usage := float64(len(this.sendChan)) / float64(cap(this.sendChan))
			if usage > 0.8 {
				log.Printf("send buffer usage: %.1f%%, consider increasing size", usage*100)
			}
			now := time.Now().UnixNano()
			heartIntervalNano := int64(heart_interval)
			// 1. 超时检查：检查距离上次 PONG 有多久
			// lastPong := this.last_pong_nano.Load()
			lastMsg := this.last_msg_nano.Load()
			// last_max_msg := lo.Max([]int64{lastPong, lastMsg})
			// log.Printf("now: %v, lastPong: %v, lastMsg: %v", time.Unix(0, now).Format(time.DateTime), time.Unix(0, lastPong).Format(time.DateTime), time.Unix(0, lastMsg).Format(time.DateTime))
			// if now-last_max_msg > heartIntervalNano*2 { //这里乘以2，给对方多一点时间响应
			// 	return fmt.Errorf("pong timeout, last pong: %v ago", time.Duration(now-lastPong))
			// }

			// 2. 发送 PING 检查：检查距离上次业务消息有多久
			// log.Printf("delta since last msg: %v", time.Duration(now-lastMsg))
			if now-lastMsg >= heartIntervalNano {
				log.Printf("send ping msg:%v", time.Unix(0, now).Format(time.DateTime))
				if err = this.ping(); err != nil {
					this.Close()
					return
				}
				time.AfterFunc(heart_interval, func() {
					// 再次检查是否收到 pong
					lastPong := this.last_pong_nano.Load()
					if this.closed.Load() {
						return
					}
					if time.Now().UnixNano()-lastPong > heartIntervalNano {
						log.Printf("pong not received in time, closing connection")
						this.Close()
					}
				})
			}
		}
	}
}

func (this *Conn) readPump() error {
	// readDeadline := *this.opt.HeartInterval*2 + 5*time.Second
	// opt := Options().SetReadDeadline(readDeadline)
	for {
		select {
		case <-this.done:
			return fmt.Errorf("connection closed")
		default:
			flag, body, err := this.read()
			if err != nil {
				// if netErr, ok := err.(net.Error); ok && netErr.Timeout() { //把探测死链接移动到 writePump 里处理
				// 	continue
				// }
				return fmt.Errorf("1:%w", err)
			}
			switch flag {
			case flag_msg:
				this.last_msg_nano.Store(time.Now().UnixNano())
				if this.handler != nil {
					func() {
						defer func() {
							if r := recover(); r != nil {
								log.Printf("handler panic: %v", r)
							}
						}()
						if err := this.handler.HandleMsg(body); err != nil {
							log.Printf("handle msg error: %v", err)
						}
					}()
				}
			case flag_ping:
				if this.closed.Load() {
					return fmt.Errorf("connection closed")
				}
				log.Println("receive ping msg")
				if err := this.pong(); err != nil {
					return err
				}
			case flag_pong:
				this.last_pong_nano.Store(time.Now().UnixNano())
				log.Println("receive pong msg")
				// 正常处理，时间已更新
			default:
				return fmt.Errorf("unknown flag: %d", flag)
			}
		}
	}
}
