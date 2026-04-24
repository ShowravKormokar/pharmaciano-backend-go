package rbac

import (
	"log"
	"sync"

	"backend/internal/database"

	"github.com/casbin/casbin/v2"
	gormadapter "github.com/casbin/gorm-adapter/v3"
)

var (
	enforcer *casbin.Enforcer
	once     sync.Once
)

func Init() {
	once.Do(func() {

		// use correct adapter
		adapter, err := gormadapter.NewAdapterByDB(database.DB)
		if err != nil {
			log.Fatalf("❌ Casbin adapter: %v", err)
		}

		// correct model path
		e, err := casbin.NewEnforcer("./internal/rbac/model.conf", adapter)
		if err != nil {
			log.Fatalf("❌ Casbin enforcer: %v", err)
		}

		// IMPORTANT
		if err := e.LoadPolicy(); err != nil {
			log.Fatalf("❌ LoadPolicy failed: %v", err)
		}

		enforcer = e
		log.Println("✅ Casbin initialized")
	})
}

func ensureInit() {
	if enforcer == nil {
		Init()
	}
}

func Enforce(role, module, action string) bool {
	if role == "Super_Admin" {
		return true
	}
	ensureInit()
	ok, _ := enforcer.Enforce(role, module, action)
	return ok
}

func AddPolicy(role, module, action string) error {
	ensureInit()

	exists := enforcer.HasPolicy(role, module, action)
	if exists {
		return nil
	}

	_, err := enforcer.AddPolicy(role, module, action)
	return err
}

func ClearPolicies() {
	ensureInit()
	enforcer.ClearPolicy()
}

func SavePolicies() {
	ensureInit()
	if err := enforcer.SavePolicy(); err != nil {
		log.Println("❌ SavePolicy failed:", err)
	}
}
