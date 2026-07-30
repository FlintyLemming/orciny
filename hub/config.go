package hub

import (
	"time"

	"github.com/blang/semver/v4"

	"github.com/FlintyLemming/orciny"
	"github.com/FlintyLemming/orciny/internal/clock"
)

// Config 收拢 hub 的全部可调参数。
//
// 所有时长都可注入，是为了让测试把 70 秒的 deadline 换成 200 毫秒——
// gws 的读写 deadline 走的是真实网络时间，假时钟对它无效（spec §12.3 的边界）。
type Config struct {
	// DataDir 仅对 New 有意义；Attach 的实例用宿主 app 的数据目录。
	DataDir string

	Clock clock.Clock

	OfflineGrace      time.Duration // 连接断开到判定 offline 的宽限，spec §6.3
	HeartbeatInterval time.Duration // hub 主动 ping 间隔，spec §6.2
	ReadTimeout       time.Duration // WS read deadline，spec §6.2
	HandshakeTimeout  time.Duration // 从 WS 升级到收到合法 Auth，spec §4.2
	EnrollTokenTTL    time.Duration // 注册 token 有效期，spec §4.4

	MinAgentVersion semver.Version

	// DownloadBase 是 install.sh 的下载源。空值走 routes.DefaultDownloadBase
	// （GitHub releases）；开发期指向本地构建产物，免得每次都要发 release
	// 才能测安装路径（spec §11.4）。
	//
	// 这里不在 WithDefaults 里补默认：默认值属于 routes 包，
	// 让 hub 复制一份常量只会多一处需要同步的地方。
	DownloadBase string
}

// WithDefaults 返回补齐默认值后的副本。零值即默认，调用方不必知道默认是多少。
func (c Config) WithDefaults() Config {
	if c.Clock == nil {
		c.Clock = clock.System()
	}
	if c.OfflineGrace == 0 {
		c.OfflineGrace = 5 * time.Second
	}
	if c.HeartbeatInterval == 0 {
		c.HeartbeatInterval = 30 * time.Second
	}
	if c.ReadTimeout == 0 {
		c.ReadTimeout = 70 * time.Second
	}
	if c.HandshakeTimeout == 0 {
		c.HandshakeTimeout = 10 * time.Second
	}
	if c.EnrollTokenTTL == 0 {
		c.EnrollTokenTTL = 15 * time.Minute
	}
	if c.MinAgentVersion.EQ(semver.Version{}) {
		c.MinAgentVersion = orciny.MinAgentVersion
	}
	return c
}
