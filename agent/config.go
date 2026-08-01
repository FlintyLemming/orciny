package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/FlintyLemming/orciny/internal/atomicfile"
)

// ConfigFileName 是 agent 配置在 agent 目录下的文件名。
const ConfigFileName = "agent.yml"

// Config 是 ~/.orciny/agent.yml 的内容（spec §7.1）。
// 安装时生成，一般不手改。
type Config struct {
	HubURL    string `yaml:"hub_url"`
	MachineID string `yaml:"machine_id"`
	// LogFile 为 true 时额外把日志写到 ~/.orciny/logs/（spec §7.5）。
	LogFile bool `yaml:"log_file"`

	// ManagedHome 是**被管理的** HOME，即 ~/.claude 所在的那个 home
	// （spec §2.3）。空 = os.UserHomeDir()。
	//
	// 它与 $ORCINY_HOME（agent 自己的数据目录）必须分开：测试要把两者都
	// 放临时目录，否则一个集成测试会去动开发者本人的 ~/.claude。
	// 安装脚本不写这个字段。
	ManagedHome string `yaml:"managed_home"`

	// ReconcileInterval 是定时全量对账周期（spec §8.1）。空 = 5m。
	ReconcileInterval string `yaml:"reconcile_interval"`
}

// DefaultDir 返回 agent 的工作目录：$ORCINY_HOME 或 ~/.orciny。
// 环境变量优先，测试与多实例部署靠它隔离。
func DefaultDir() string {
	if d := os.Getenv("ORCINY_HOME"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		// 拿不到 home 时退回相对目录，让错误在后续读写时暴露得更具体。
		return ".orciny"
	}
	return filepath.Join(home, ".orciny")
}

func LoadConfig(dir string) (*Config, error) {
	b, err := os.ReadFile(filepath.Join(dir, ConfigFileName))
	if err != nil {
		return nil, err // 保留 os.ErrNotExist，调用方用 errors.Is 判断「尚未 enroll」
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("解析 %s: %w", ConfigFileName, err)
	}
	return &c, nil
}

func SaveConfig(dir string, c *Config) error {
	b, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("序列化配置: %w", err)
	}
	return atomicfile.Write(filepath.Join(dir, ConfigFileName), b, 0o600)
}

// DefaultReconcileInterval 是定时全量对账的默认周期（spec §8.1）。
const DefaultReconcileInterval = 5 * time.Minute

// minReconcileInterval 挡住手抖写成 1s 的配置：对账要遍历整棵 skills 子树，
// 太密会把空闲 CPU 从「≈0」变成常驻百分之几。
const minReconcileInterval = 10 * time.Second

// ManagedHomeDir 返回被管理的 HOME。
func (c *Config) ManagedHomeDir() (string, error) {
	if c.ManagedHome != "" {
		return c.ManagedHome, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("取用户 home 失败，请在 %s 里显式设置 managed_home: %w",
			ConfigFileName, err)
	}
	return home, nil
}

// ReconcileEvery 解析对账周期。解不出来或过小时退回安全值——
// 一个手抖的配置不该把对账整个关掉，也不该把 CPU 烧掉。
func (c *Config) ReconcileEvery() time.Duration {
	if c.ReconcileInterval == "" {
		return DefaultReconcileInterval
	}
	d, err := time.ParseDuration(c.ReconcileInterval)
	if err != nil {
		return DefaultReconcileInterval
	}
	if d < minReconcileInterval {
		return minReconcileInterval
	}
	return d
}
