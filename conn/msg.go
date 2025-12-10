package conn

import "sync"

type msg struct {
	flag byte
	data [][]byte
	opt  *Option
}

var msgPool = sync.Pool{
	New: func() any { return &msg{} },
}

func (m *msg) reset() {
	m.flag = 0
	m.data = nil
	m.opt = nil
}

func (m *msg) Release() {
	if m == nil {
		return
	}
	m.reset()
	msgPool.Put(m)
}
