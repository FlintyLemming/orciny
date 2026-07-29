package conn

// 本文件只在测试构建里存在，用来把「造一条没有真实 socket 的会话」这件事
// 暴露给外部测试包 conn_test，而不必把它留在生产代码的对外接口上。

// NewTestSession 造一条不带真实 socket 的会话。
// 它不能发消息，只能被关闭——重连循环关心的正是「什么时候结束」。
func NewTestSession() *Session {
	return &Session{done: make(chan struct{})}
}

// CloseForTest 模拟连接因 err 结束。
func (s *Session) CloseForTest(err error) { s.finish(err) }
