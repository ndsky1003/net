package logger

import (
	"fmt"
	"log"
	"os"
)

// Logger 抽象接口 (保持不变)
// 建议：为了通用性，args ...any 是标准库 log 的风格
type Logger interface {
	Debugf(format string, args ...any)
	Debug(msg string)
	Infof(format string, args ...any)
	Info(args ...any) // 修改为适配 log.Println 风格
	Warnf(format string, args ...any)
	Warn(args ...any)
	Errorf(format string, args ...any)
	Error(args ...any)
}

// -------------------------------------------------------
// 全局访问入口
// -------------------------------------------------------

var globalLogger Logger = newStdLogger()

func SetGlobalLogger(l Logger) {
	if l != nil {
		globalLogger = l
	}
}

// -------------------------------------------------------
// 包级快捷函数 (Proxy)
// -------------------------------------------------------

func Debugf(format string, args ...any) { globalLogger.Debugf(format, args...) }
func Debug(msg string)                  { globalLogger.Debug(msg) }

func Infof(format string, args ...any) { globalLogger.Infof(format, args...) }
func Info(args ...any)                 { globalLogger.Info(args...) }

func Warnf(format string, args ...any) { globalLogger.Warnf(format, args...) }
func Warn(args ...any)                 { globalLogger.Warn(args...) }

func Errorf(format string, args ...any) { globalLogger.Errorf(format, args...) }
func Error(args ...any)                 { globalLogger.Error(args...) }

// -------------------------------------------------------
// 默认实现：基于 std/log 的分级优化版
// -------------------------------------------------------

type stdLogger struct {
	debug *log.Logger
	info  *log.Logger
	warn  *log.Logger
	error *log.Logger
}

func newStdLogger() *stdLogger {
	flags := log.LstdFlags | log.Lshortfile | log.Lmicroseconds
	// 预先创建带前缀的 logger，避免每次打印时拼接字符串或分配切片
	return &stdLogger{
		debug: log.New(os.Stderr, "[DEBUG] ", flags),
		info:  log.New(os.Stderr, "[INFO]  ", flags),
		warn:  log.New(os.Stderr, "[WARN]  ", flags),
		error: log.New(os.Stderr, "[ERROR] ", flags),
	}
}

// calldepth 为 2，是为了跳过 stdLogger.Info 和 globalLogger.Info 这两层
// 确保日志里打印的文件名是调用者的位置
const calldepth = 2

// Debugf / Debug 默认留空，生产环境通常不需要
func (l *stdLogger) Debugf(format string, args ...any) {}
func (l *stdLogger) Debug(msg string)                  {}

// --- INFO ---

func (l *stdLogger) Infof(format string, args ...any) {
	// Output 内部直接写入，自带前缀，无额外切片分配
	l.info.Output(calldepth, fmt.Sprintf(format, args...))
}

func (l *stdLogger) Info(args ...any) {
	// 使用 fmt.Sprint 将 args 格式化为字符串，传递给 Output
	// 这是标准库 log.Println 的内部实现方式，我们复刻它但修正了 call depth
	l.info.Output(calldepth, fmt.Sprint(args...))
}

// --- WARN ---

func (l *stdLogger) Warnf(format string, args ...any) {
	l.warn.Output(calldepth, fmt.Sprintf(format, args...))
}

func (l *stdLogger) Warn(args ...any) {
	l.warn.Output(calldepth, fmt.Sprint(args...))
}

// --- ERROR ---

func (l *stdLogger) Errorf(format string, args ...any) {
	l.error.Output(calldepth, fmt.Sprintf(format, args...))
}

func (l *stdLogger) Error(args ...any) {
	l.error.Output(calldepth, fmt.Sprint(args...))
}
