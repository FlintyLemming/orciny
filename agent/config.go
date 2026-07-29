package agent

import (
	"fmt"
	"os"
	"path/filepath"

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
