package migrations

import (
	"errors"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(up005, down005, "005_drop_credentials.go")
}

// M1.6 的破坏性一半（spec §6.1 的第 3、4、5 步）。
//
// 跑这条之前，004 已经把每条 provider 的旧字段与它引用的凭据搬进了新结构。
// 这里只做拆除：删 relation 与索引、删四个旧字段、删 credentials collection。
func up005(app core.App) error {
	provs, err := app.FindCollectionByNameOrId("providers")
	if err != nil {
		return err
	}
	// 索引要先于字段删——PocketBase 不允许索引指向不存在的列。
	provs.RemoveIndex("idx_providers_credential")
	for _, f := range []string{
		"credential", "base_url", "auth_field", "models", "defaults",
	} {
		provs.Fields.RemoveByName(f)
	}
	if err := app.Save(provs); err != nil {
		return err
	}

	creds, err := app.FindCollectionByNameOrId("credentials")
	if err != nil {
		return nil // 已经没了，幂等
	}
	return app.Delete(creds)
}

// down005 直接返回错误（spec §6.3）。
//
// 凭据删了之后无处还原——004 的搬运是**有损的**：多条凭据可能被同一条
// provider 引用过，反向拆不回去。写一个假装能回滚的 down 比没有更危险。
func down005(core.App) error {
	return errors.New("005: 不支持回滚——凭据实体已删除，无处还原。" +
		"若要回到 M1.5，请从迁移前的备份恢复整个 pb_data")
}

// Down005 供测试断言「它确实拒绝回滚」。
func Down005(app core.App) error { return down005(app) }
