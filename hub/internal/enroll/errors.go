package enroll

import "errors"

// 这些错误决定 HTTP 状态码与 agent 的下一步动作，因此必须可 errors.Is。
// 对外的错误信息刻意保持粗粒度：不告诉调用方「token 存在但过期了」还是
// 「token 根本不存在」，避免变成探测接口。
var (
	// ErrTokenInvalid 表示 token 不存在或格式不对。
	ErrTokenInvalid = errors.New("enroll: 注册 token 无效")
	// ErrTokenExpired 表示 token 已过期。
	ErrTokenExpired = errors.New("enroll: 注册 token 已过期")
	// ErrTokenUsed 表示 token 已被核销，且本次请求不构成幂等重放。
	ErrTokenUsed = errors.New("enroll: 注册 token 已被使用")
	// ErrBadPubKey 表示请求里的公钥无法解析。
	ErrBadPubKey = errors.New("enroll: 公钥格式错误")
	// ErrBadRequest 表示必填字段缺失。
	ErrBadRequest = errors.New("enroll: 请求字段不完整")
)
