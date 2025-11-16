package conn

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"
)

type Conn struct {
	net.Conn
	r   io.Reader
	w   *bufio.Writer
	opt *Option
}

func New(conn net.Conn, opts ...*Option) *Conn {
	opt := Options().SetDeadline(10 * time.Second).SetMaxFrameSize(64 * 1024).Merge(opts...)
	return &Conn{
		Conn: conn,
		r:    bufio.NewReader(conn),
		w:    bufio.NewWriter(conn),
		opt:  opt,
	}
}

// WARNING: 这个函数是非线程安全的，需要外部调用者保证
func (this *Conn) WriteFrame(data []byte, opts ...*Option) (err error) {
	opt := Options().Merge(this.opt).Merge(opts...)
	length := len(data)
	var size [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(size[:], uint64(length))
	if err = this.Conn.SetWriteDeadline(time.Now().Add(*opt.WriteDeadline)); err != nil {
		return
	}
	if _, err = this.w.Write(size[:n]); err != nil {
		return
	}
	if _, err = this.w.Write(data); err != nil {
		return
	}
	return this.w.Flush()
}

func (this *Conn) ReadFrame(opts ...*Option) (data []byte, err error) {
	opt := Options().Merge(this.opt).Merge(opts...)
	max_frame_size := *opt.MaxFrameSize
	this.Conn.SetReadDeadline(time.Now().Add(*opt.ReadDeadline))
	size, err := binary.ReadUvarint(this.r.(io.ByteReader))
	if err != nil {
		return nil, err
	}
	if size > max_frame_size {
		return nil, fmt.Errorf("frame size %d exceeds maximum %d", size, max_frame_size)
	}
	if size == 0 {
		return nil, nil
	}
	if size != 0 {
		data = make([]byte, size)
		if _, err = io.ReadFull(this.r, data); err != nil {
			return nil, err
		}
	}
	return
}

func (this *Conn) Close() error {
	return this.Conn.Close()
}
