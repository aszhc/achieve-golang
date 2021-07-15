package context

import "sync"

type CancelFunc func()

// cancelCtx 可取消的Context, 当它被取消 它的孩子context节点也会被取消
type cancelCtx struct {
	Context // *** 匿名interface 使得cancelCtx可以不实现interface的方法

	mu       sync.Mutex            // 保护这几个字段的锁，保证线程安全
	done     chan struct{}         // 用于获取该Context的取消通知
	children map[canceler]struct{} // 记录了由此context派生的所有child 此context被取消时会把其中所有的child都`cancel`掉
	err      error                 // 当被cancel时将会把err设置为 非nil
}

// Done ** 2 **
// 返回的是一个只读的chan
func (c *cancelCtx) Done() <-chan struct{} {
	c.mu.Lock()
	if c.done == nil {
		c.done = make(chan struct{})
	}
	d := c.done
	c.mu.Unlock()
	return d
}

// Err ** 3 **
func (c *cancelCtx) Err() error {
	c.mu.Lock()
	err := c.err
	c.mu.Unlock()
	return err
}

// &cancelCtxKey 是一个用于返回它本身的key
var cancelCtxKey int

// Value ** 4 **
func (c *cancelCtx) Value(key interface{}) interface{} {
	if key == &cancelCtxKey {
		return c
	}
	return c.Context.Value(key)
}

func (c *cancelCtx) String() string {
	return contextName(c.Context) + ".WithCancel"
}

// 关闭自己及其后代
// 核心是关闭c.done
// 同时会设置c.err = err, c.children = nil
// 依次遍历c.children, 每个child分别cancel
// 如果设置了removeFromParent, 则将c从parent的children中删除
// ** 该方法会关闭上下文中的 Channel 并向所有的子上下文同步取消信号 **
// cancel() 方法的功能就是关闭 channel：c.done
// 达到的效果就是通过关闭channel，将取消信号传递给它的所有子节点
// goroutine 接收到取消信号的方式就是 select 语句中的读 c.done 被选中
//
// 当removeFromParent为true时，会将当前节点的context从父节点context中删除
func (c *cancelCtx) cancel(removeFromParent bool, err error) {
	// 必须要传err
	if err == nil {
		panic("context: internal error: missing cancel error")
	}
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return //已经被其他协程取消
	}
	// 给err字段赋值
	c.err = err
	// 关闭channel， 通知其他协程
	if c.done == nil { // 不管怎样，close掉c.done
		c.done = closedchan
	} else {
		close(c.done)
	}
	// 遍历它的所有子节点
	for child := range c.children {
		// 递归，cancel掉孩子节点
		child.cancel(false, err)
	}
	// 将子节点置空
	c.children = nil
	c.mu.Unlock()

	if removeFromParent {
		// 从父节点中移除自己
		removeChild(c.Context, c)
	}
}

// removeChild 从它们的parent context中删除context
// 去掉父节点的孩子节点
func removeChild(parent Context, child canceler) {
	p, ok := parentCancelCtx(parent)
	if !ok {
		return
	}
	p.mu.Lock()
	if p.children != nil {
		delete(p.children, child) // ??
	}
	p.mu.Unlock()
}

// parentCancelCtx 从父节点中返回潜在的*cancelCtx 就是找到第一个可取消的父节点
func parentCancelCtx(parent Context) (*cancelCtx, bool) {
	done := parent.Done()
	if done == closedchan || done == nil {
		return nil, false
	}
	p, ok := parent.Value(&cancelCtxKey).(*cancelCtx) // ??  什么写法
	if !ok {
		return nil, false
	}
	p.mu.Lock()
	ok = p.done == done
	p.mu.Unlock()
	if !ok {
		return nil, false
	}
	return p, true
}
