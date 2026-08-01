package protocol

// M1 的全部消息（spec §5.2）。字段编号只增不改不复用。

// 尺寸上限（spec §5.4）。两侧共用同一份常量。
const (
	// MaxFileSize 是单个受管文件的上限。超限：采集时跳过并记入 Skipped，
	// 发布时校验拒绝，漂移时只报路径并置 Truncated。
	MaxFileSize = 512 << 10
	// MaxBatchSize 是携带内容的消息的分批阈值，最后一批置 Final。
	MaxBatchSize = 256 << 10
	// MaxPayload 是 WS 的 ReadMaxPayloadSize，hub 与 agent 两侧都要显式设。
	MaxPayload = 1 << 20
)

// ApplyResult.Action
const (
	ActionSkip      uint8 = 1
	ActionCreate    uint8 = 2
	ActionOverwrite uint8 = 3
	ActionDelete    uint8 = 4
	ActionMerge     uint8 = 5
)

// DriftItem.Kind
const (
	DriftAdded    uint8 = 1
	DriftModified uint8 = 2
	DriftDeleted  uint8 = 3
)

// ConfigSnapshot.Mode
const (
	ModeApply  uint8 = 0
	ModeSurvey uint8 = 1
)

// ConfigNotify.Reason
const (
	ReasonPublished = "published"
	ReasonAdopted   = "adopted"
	ReasonRotated   = "rotated"
	ReasonAssigned  = "assigned"
)

// DriftCommand.Op
const (
	OpRestore = "restore"
	OpIgnore  = "ignore"
)

// 跳过原因（CollectResult.Skipped 与 manifest 展开共用）
const (
	SkipTooLarge       = "too_large"
	SkipNotRegular     = "not_regular"
	SkipAlwaysExcluded = "always_excluded"
	SkipUnreadable     = "unreadable"
)

// ConfigNotify 只发信号不带内容（spec §5.1）：凭据轮换可以复用同一条消息，
// 且不产生新 Revision。
type ConfigNotify struct {
	ConfigSetID string `cbor:"0,keyasint"`
	RevisionID  string `cbor:"1,keyasint,omitempty"` // 空 = 仅 secrets 变更
	Seq         uint32 `cbor:"2,keyasint,omitempty"`
	Reason      string `cbor:"3,keyasint,omitempty"`
}

type ConfigPull struct {
	Have string `cbor:"0,keyasint,omitempty"` // 本地已应用的 RevisionID，供 hub 记日志
}

type ConfigSnapshot struct {
	ConfigSetID string            `cbor:"0,keyasint"`
	RevisionID  string            `cbor:"1,keyasint"`
	Seq         uint32            `cbor:"2,keyasint"`
	Manifest    []byte            `cbor:"3,keyasint"` // 原样 JSON，与发布时冻结的一致
	Files       []FileEntry       `cbor:"4,keyasint"`
	Checksum    string            `cbor:"5,keyasint"`
	Credentials map[string]string `cbor:"6,keyasint,omitempty"` // 只含本 Revision 引用到的
	Variables   map[string]string `cbor:"7,keyasint,omitempty"`
	IgnorePaths []string          `cbor:"8,keyasint,omitempty"`
	Mode        uint8             `cbor:"9,keyasint,omitempty"`
}

type FileEntry struct {
	Path string   `cbor:"0,keyasint"`
	Hash string   `cbor:"1,keyasint"` // keys 模式为受管键子树的规范化 hash
	Size uint32   `cbor:"2,keyasint"`
	Mode uint32   `cbor:"3,keyasint"` // 0600 / 0644
	Keys []string `cbor:"4,keyasint,omitempty"`
}

type BlobRequest struct {
	Hashes []string `cbor:"0,keyasint"`
}

type BlobData struct {
	Hash    string `cbor:"0,keyasint"`
	Content []byte `cbor:"1,keyasint"`
	Missing bool   `cbor:"2,keyasint,omitempty"` // hub 侧找不到，agent 据此中止 apply
}

type ApplyAck struct {
	RevisionID string        `cbor:"0,keyasint"`
	OK         bool          `cbor:"1,keyasint"`
	Results    []ApplyResult `cbor:"2,keyasint,omitempty"`
	Error      string        `cbor:"3,keyasint,omitempty"`
	RolledBack bool          `cbor:"4,keyasint,omitempty"`
	DurationMs uint32        `cbor:"5,keyasint,omitempty"`
}

type ApplyResult struct {
	Path   string `cbor:"0,keyasint"`
	Action uint8  `cbor:"1,keyasint"`
	Error  string `cbor:"2,keyasint,omitempty"`
}

// DriftReport 携带**已还原为占位符**的完整内容：diff 由 hub 计算，
// 收编因此退化成纯 hub 侧操作（spec §8.2）。
type DriftReport struct {
	Items []DriftItem `cbor:"0,keyasint"`
	Final bool        `cbor:"1,keyasint,omitempty"`
	Full  bool        `cbor:"2,keyasint,omitempty"` // survey 模式的全量对账
}

type DriftItem struct {
	Path           string `cbor:"0,keyasint"`
	Kind           uint8  `cbor:"1,keyasint"`
	BaseHash       string `cbor:"2,keyasint,omitempty"`
	Content        []byte `cbor:"3,keyasint,omitempty"`
	Mode           uint32 `cbor:"4,keyasint,omitempty"`
	RestorePartial bool   `cbor:"5,keyasint,omitempty"`
	Truncated      bool   `cbor:"6,keyasint,omitempty"`
}

type DriftCommand struct {
	Op    string   `cbor:"0,keyasint"`
	Paths []string `cbor:"1,keyasint"`
}

type CollectRequest struct {
	Manifest []byte `cbor:"0,keyasint"`
	Token    string `cbor:"1,keyasint"` // 本次采集的标识，随结果回传，防串批
}

type CollectResult struct {
	Token   string          `cbor:"0,keyasint"`
	Files   []CollectedFile `cbor:"1,keyasint,omitempty"`
	Skipped []SkippedFile   `cbor:"2,keyasint,omitempty"`
	Final   bool            `cbor:"3,keyasint,omitempty"`
	Error   string          `cbor:"4,keyasint,omitempty"`
}

type CollectedFile struct {
	Path    string `cbor:"0,keyasint"`
	Content []byte `cbor:"1,keyasint"`
	Mode    uint32 `cbor:"2,keyasint"`
}

type SkippedFile struct {
	Path   string `cbor:"0,keyasint"`
	Reason string `cbor:"1,keyasint"`
}
