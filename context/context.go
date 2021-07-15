package context

import (
	"errors"
	"reflect"
	"sync/atomic"
	"time"
)

type Context interface {
	// Deadline 返回 context 是否会被取消以及自动取消时间（即 deadline）
	Deadline() (deadline time.Time, ok bool)

	// Done 当context被取消或者到了deadline， 放回一个被关闭的channel
	Done() <-chan struct{}

	// Err 在channel Done 关闭后，返回context取消的原因
	Err() error

	// Value 获取key对应的value
	Value(key interface{}) interface{}
}

// canceler 用于取消context类型
// 实现该接口的类型都可以被直接canceled
// *cancelCtx 和 *timerCtx 都实现了这个接口
type canceler interface {
	cancel(removeFromParent bool, err error)
	Done() <-chan struct{}
}

type stringer interface {
	String() string
}

var (
	background = new(emptyCtx) // 公用的emptyCtx 全局变量
	todo       = new(emptyCtx)
)

// Background 作为根Context，不能被取消
// 主要用于初始化、main函数中
func Background() Context {
	return background
}

// TODO
// 如果不知道该使用什么Context的时候，可以使用   使用场景少
func TODO() Context {
	return todo
}

// Notes: Background和TODO本质上都是emptyCtx类型, 是一个不可取消、没有设置截止时间、没有携带任何值的Context

func contextName(c Context) string {
	if s, ok := c.(stringer); ok {
		return s.String()
	}
	return reflect.TypeOf(c).String()
}

// closedchan 是一个可重复利用的关闭 channel
var closedchan = make(chan struct{})

func init() {
	close(closedchan)
}

// Canceled 是context被canceled后，Context.Err 返回的一个错误
var Canceled = errors.New("context canceled")

// WithCancel 做的事情：
//1. 初始化一个cancelCtx实例
//2. 将cancelCtx实例添加到其父节点的children中
//3. 返回cancelCtx实例和cancel方法
// 传递一个父Context作为参数(通常是background，作为根节点)，返回子Context，以及一个取消函数用来取消Context
func WithCancel(parent Context) (ctx Context, cancel CancelFunc) {
	if parent == nil {
		panic("不能没有parent context 创建 context")
	}
	c := newCancelCtx(parent)                      //  1. 初始化一个cancelCtx实例
	propagateCancel(parent, &c)                    // 构建父子上下文之间的关联，当父context被取消时，子context也会被取消
	return &c, func() { c.cancel(true, Canceled) } // 3. 返回cancelCtx实例和cancel方法
}

// newCancelCtx 返回一个初始化的 cancelCtx
func newCancelCtx(parent Context) cancelCtx {
	return cancelCtx{Context: parent}
}

// goroutines 为了计算goroutines被创建的个数； 为了测试
var goroutines int32

// 向下传递context节点间的取消关系
func propagateCancel(parent Context, child canceler) {
	done := parent.Done()
	// 父节点是个空节点
	if done == nil { // 1. 当parent不会触发取消事件时，当前函数会直接返回
		return // 父context不会触发取消信号
	}

	select {
	case <-done:
		child.cancel(false, parent.Err()) // 父context已经被取消时，子context会被立刻取消
		return
	default:
	}
	// 找到可以取消父context
	if p, ok := parentCancelCtx(parent); ok {
		p.mu.Lock()
		if p.err != nil {
			// 父context已经被取消，本节点也要取消
			child.cancel(false, p.err)
		} else {
			// 父节点未取消
			if p.children == nil {
				p.children = make(map[canceler]struct{})
			}
			// "挂到"父节点上
			p.children[child] = struct{}{}
		}
		p.mu.Unlock()
	} else {
		atomic.AddInt32(&goroutines, +1)
		// 如果没有找到可取消的父 context。新启动一个协程监控父节点或子节点取消信号
		go func() {
			select {
			case <-parent.Done(): // 当parent.Done() 关闭时调用child.cancel 取消上下文
				child.cancel(false, parent.Err())
			case <-child.Done():
			}
		}()
	}

}

var DeadlineExceeded error = deadlineExceededError{}

type deadlineExceededError struct{}

func (deadlineExceededError) Error() string   { return "context deadline exceeded" }
func (deadlineExceededError) Timeout() bool   { return true }
func (deadlineExceededError) Temporary() bool { return true }

// WithDeadline 比WithCancel多传递一个截止时间参数
func WithDeadline(parent Context, d time.Time) (Context, CancelFunc) {
	if parent == nil {
		panic("cannot create context from nil parent")
	}
	if cur, ok := parent.Deadline(); ok && cur.Before(d) {
		return WithCancel(parent)
	}
	c := &timerCtx{
		cancelCtx: newCancelCtx(parent),
		deadline:  d,
	}
	propagateCancel(parent, c)
	dur := time.Until(d)
	if dur <= 0 {
		c.cancel(true, DeadlineExceeded)
		return c, func() { c.cancel(false, Canceled) }
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err == nil {
		c.timer = time.AfterFunc(dur, func() {
			c.cancel(true, DeadlineExceeded)
		})
	}
	return c, func() { c.cancel(true, Canceled) }
}

// WithTimeout 和WithDeadline基本一样，表示超时自动取消
func WithTimeout(parent Context, timeout time.Duration) (Context, CancelFunc) {
	return WithDeadline(parent, time.Now().Add(timeout))
}

// WithValue
// 和取消Context无关， 它是为了生成一个绑定了一个键值对数据的Context， 这个绑定数据可以通过Context.Value方法访问
// 如果我们想要通过上下文传递数据时，可以通过这个方法
func WithValue(parent Context, key, val interface{}) Context {
	if parent == nil {
		panic("cannot create context from nil parent")
	}
	if key == nil {
		panic("nil key")
	}
	if !reflect.TypeOf(key).Comparable() {
		panic("key is not comparable")
	}
	return &valueCtx{parent, key, val}
}
