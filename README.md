# Pharmaciano Backend (Go)

🚀 **Pharmaciano Backend** is an enterprise-grade ERP backend system built with **Go**, designed specifically for **pharmacy and healthcare retail management**.  
It provides secure, scalable, and high-performance REST APIs to support real-world pharmacy operations such as inventory, sales, users, roles, and reporting.

**Notice:** This is still under construction, time period **Jan'26-July'26**.🚨🚨☠️☠️

---

## 🧩 Project Overview

Pharmaciano is a **full-stack ERP system** where this repository represents the **backend service**, responsible for:

- Business logic
- Data persistence
- Authentication & authorization
- Role + Permission Based Access Control
- Caching & background jobs
- Monitoring & observability
- Rate limiting

The backend is designed using **clean architecture principles** to ensure scalability, maintainability, and long-term growth.

---

## 🖥 Frontend Repository

👉 **Frontend (Next.js):**  
🔗 https://github.com/showravkormokar/Pharmaciano

---

# 🛠 Tech Stack

## 🔹 Core Backend
- **Language:** Go (Golang)
- **Framework:** Gin (HTTP REST framework)

## 🔹 Database & Cache
- **Primary Database:** PostgreSQL
- **ORM:** GORM
- **Caching / Queue Backend:** Redis

## 🔹 Authentication & Security
- **Authentication:** JWT (Access Token + Refresh Token)
- **Authorization:** RBAC using Casbin
- **Password Hashing:** bcrypt

## 🔹 Background Processing
- **Async Jobs:** Asynq (Redis-based background job processing)

## 🔹 Validation & Utilities
- **Request Validation:** go-playground/validator
- **Configuration Management:** godotenv

## 🔹 Observability & Monitoring
- **Logging:** Zap (structured logging)
- **Metrics:** Prometheus
- **Visualization:** Grafana

## 🔹 Database Migrations
- **Migration Tool:** golang-migrate

---

## 📁 Project Structure
- **Architecture:** Hexagonal Architecture + Layered Architecture = **GO-style Clean Architecture**.
```text
pharmaciano-backend-go/
backend/
├── cmd/
│   └── server/
│       └── main.go             # Application entrypoint
│
├── internal/
│   ├── config/
│   │   └── config.go           # Viper-based config loader (YAML/env)
│   │
│   ├── domain/                 # Domain interfaces (optional layer)
│   │   ├── user/
│   │   │   ├── entity.go       # Domain user (Business model)
│   │   │   ├── repository.go   # IUserRepository interface
│   │   │   └── service.go      # IUserService interface (business logic)
│   │   └── ... (inventory, sales, etc.)
│   │
│   ├── models/                 # GORM models (DB schema)
│   │   └── (all model structs) │
│   │
│   ├── dto/                    # Request/Response structs
│   │   ├── user_dto.go
│   │   ├── sale_dto.go
│   │   └── ...                 │
│   │
│   ├── repository/             # Repository implementations
│   │   ├── user_repo.go
│   │   ├── sale_repo.go
│   │   └── ...                 │
│   │
│   ├── services/               # Business logic (use cases)
│   │   ├── user_service.go
│   │   ├── sale_service.go
│   │   ├── inventory_service.go
│   │   ├── report_service.go
│   │   └── ai_service.go
│   │
│   ├── handlers/               # HTTP handlers (thin)
│   │   ├── auth_handler.go
│   │   ├── user_handler.go
│   │   ├── sale_handler.go
│   │   └── ...                 │
│   │
│   ├── routes/
│   │   ├── routes.go           # RegisterRoutes, version groups
│   │   ├── v1/                 # v1 route files (user, sale, etc.)
│   │   └── v2/                 # v2 route files (future)
│   │
│   ├── middlewares/
│   │   ├── auth_middleware.go  # JWT auth (with signing check)
│   │   ├── rbac_middleware.go  # Calls Casbin to enforce perms
│   │   ├── tenant_middleware.go# Adds org scope to context (for DB)
│   │   ├── audit_middleware.go # Logs requests to audit table
│   │   ├── rate_limit.go       # Uses ulule/limiter
│   │   └── security_headers.go # Sets HTTP hardening headers
│   │
│   ├── auth/
│   │   ├── jwt.go              # JWT generation/parsing (fixed alg bug)
│   │   └── password.go         # bcrypt wrapper, hash/verify
│   │
│   ├── rbac/
│   │   └── casbin.go           # Casbin enforcer init (with gorm-adapter)
│   │
│   ├── cache/                  # Redis clients, key constants
│   │   ├── redis.go
│   │   └── keys.go             # e.g. "token_blacklist:%s"
│   │
│   ├── database/
│   │   ├── postgres.go         # Connect GORM, set pool
│   │   └── migrate.go          # GORM AutoMigrate or integration with migrate tool
│   │
│   ├── jobs/                   # Background jobs (Asynq server)
│   │   ├── worker.go           # Asynq server setup
│   │   ├── tasks/
│   │   │   ├── report_task.go
│   │   │   ├── notification_task.go
│   │   │   └── ai_task.go
│   │   └── scheduler.go        # Cron for periodic tasks
│   │
│   ├── errors/                 # Domain error types (with HTTP codes)
│   │   └── errors.go
│   │
│   └── logger/
│       └── zap.go             # Zap logger setup
│
├── pkg/                       # Optional shared utilities
│   ├── pagination/
│   │   └── pagination.go
│   ├── response/
│   │   └── response.go         # Standard API response wrappers
│   └── validator/
│       └── validator.go       # For custom validation
│
├── scripts/
│   └── seed.go               # DB seeding scripts
│
├── migrations/               # SQL migration files (e.g. with golang-migrate)
│   ├── 001_initial.up.sql
│   ├── 001_initial.down.sql
│   └── ...
│
├── tests/
│   ├── unit/
│   └── integration/
│
├── deployments/
│   ├── docker/
│   │   └── Dockerfile
│   ├── nginx/
│   │   └── nginx.conf        # Example LB config
│   └── k8s/
│       └── deployment.yaml
│
├── .env
├── .env.example
├── go.mod
└── go.sum
```
---
## 🏗 Architecture Patterns Used

| Pattern Name | Description |
|--------------|-------------|
| **Hexagonal Architecture** | Interfaces in `domain/`; `repository/` and `handlers/` are adapters. Business logic is isolated from the outside world. |
| **Layered Architecture** | Classic 4-layer pattern: Handler → Service → Repository → DB. |
| **Repository Pattern** | Database operations hidden behind interfaces. |
| **Dependency Injection (DI)** | Services injected into handlers, repositories into services (e.g., in `main.go`). |
| **Middleware Pattern** | HTTP chain of responsibility. |
| **DTO Pattern** | Separate models from API request/response structs. |

> **Most accurate answer:** This is a hybrid of **Hexagonal Architecture + Layered Architecture**, often called **"Go-style Clean Architecture"**.
---

## Showrav Kormokar 💙 