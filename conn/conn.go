package conn

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
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
	r         io.Reader
	w         *bufio.Writer
	sendChan  chan *msg
	pong_time int64
	handler   Handler
	opt       *Option

	l    sync.Mutex //protect under
	done chan struct{}
}

func New(conn net.Conn, handler Handler, opts ...*Option) *Conn {
	opt := Options().
		SetDeadline(10 * time.Second).
		SetHeartInterval(30 * time.Second).
		SetSendChanSize(100).
		SetMaxFrameSize(64 * 1024).
		Merge(opts...)
	return &Conn{
		Conn:     conn,
		r:        bufio.NewReader(conn),
		w:        bufio.NewWriter(conn),
		handler:  handler,
		opt:      opt,
		done:     make(chan struct{}),
		sendChan: make(chan *msg, *opt.SendChanSize),
	}
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

func (this *Conn) read(opts ...*Option) (flag byte, data []byte, err error) {
	opt := Options().Merge(this.opt).Merge(opts...)
	max_frame_size := *opt.MaxFrameSize
	this.Conn.SetReadDeadline(time.Now().Add(*opt.ReadDeadline))
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

func (this *Conn) Send(data []byte, opts ...*Option) error {
	this.l.Lock()
	sendChan := this.sendChan
	this.l.Unlock()
	if sendChan == nil {
		return fmt.Errorf("connection closed")
	}
	select {
	case sendChan <- &msg{
		flag: flag_msg,
		data: data,
		opt:  Options().Merge(this.opt).Merge(opts...),
	}:
	default:
		return fmt.Errorf("send buffer full")
	}
	return nil
}

func (this *Conn) ping() error {
	if err := this.write(flag_ping, nil); err != nil {
		return fmt.Errorf("ping:%w", err)
	}
	return nil
}
func (this *Conn) pong() error {
	if err := this.write(flag_pong, nil); err != nil {
		return fmt.Errorf("pong:%w", err)
	}
	return nil
}

func (this *Conn) Serve() error {
	defer this.Close()
	errCh := make(chan error, 2)

	go func() {
		errCh <- this.writePump()
	}()

	go func() {
		errCh <- this.readPump()
	}()

	// 返回第一个错误
	return <-errCh
}

func (this *Conn) writePump() (err error) {
	atomic.StoreInt64(&this.pong_time, time.Now().UnixNano())
	ticker := time.NewTicker(*this.opt.HeartInterval)
	defer ticker.Stop()
	this.l.Lock()
	sendChan := this.sendChan
	this.l.Unlock()
	for {
		select {
		case <-this.done:
			return
		case msg, ok := <-sendChan:
			if !ok {
				return
			}
			if err = this.write(msg.flag, msg.data, msg.opt); err != nil {
				return
			}
		case <-ticker.C:
			usage := float64(len(sendChan)) / float64(cap(sendChan))
			if usage > 0.8 {
				log.Printf("send buffer usage: %.1f%%, consider increasing size", usage*100)
			}
			now := time.Now()
			if now.Sub(time.Unix(0, atomic.LoadInt64(&this.pong_time))) > *this.opt.HeartInterval {
				err = fmt.Errorf("pong timeout")
				return
			}
			if err = this.ping(); err != nil {
				return
			}
		}
	}
}

func (this *Conn) readPump() (err error) {
	for err == nil {
		select {
		case <-this.done:
			return
		default:
			var flag byte
			var body []byte
			flag, body, err = this.read()
			atomic.StoreInt64(&this.pong_time, time.Now().UnixNano())
			if flag&flag_msg == flag_msg && this.handler != nil {
				go this.handler.HandleMsg(body)
			} else {
				err = this.pong()
			}
		}
	}
	return
}

func (this *Conn) Close() {
	this.l.Lock()
	defer this.l.Unlock()
	if this.done == nil {
		return
	}
	close(this.done)
	this.Conn.Close()
	close(this.sendChan)

	this.done = nil
	this.sendChan = nil
}
