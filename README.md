# Pharmaciano Backend (Go)

🚀 **Pharmaciano Backend** is an enterprise-grade ERP backend system built with **Go**, designed specifically for **pharmacy and healthcare retail management**.  
It provides secure, scalable, and high-performance REST APIs to support real-world pharmacy operations such as inventory, sales, users, roles, and reporting.

**Notice:** This is still under construction, time period **Jan'26-April'26**.🚨🚨☠️☠️

---

## 🧩 Project Overview

Pharmaciano is a **full-stack ERP system** where this repository represents the **backend service**, responsible for:

- Business logic
- Data persistence
- Authentication & authorization
- Caching & background jobs
- Monitoring & observability

The backend is designed using **clean architecture principles** to ensure scalability, maintainability, and long-term growth.

---

## 🖥 Frontend Repository

👉 **Frontend (Next.js):**  
🔗 https://github.com/showravkormokar/Pharmaciano

---

## 🛠 Tech Stack

### 🔹 Core Backend
- **Language:** Go (Golang)
- **Framework:** Gin (HTTP REST framework)

### 🔹 Database & Cache
- **Primary Database:** PostgreSQL
- **ORM:** GORM
- **Caching / Queue Backend:** Redis

### 🔹 Authentication & Security
- **Authentication:** JWT (Access Token + Refresh Token)
- **Authorization:** RBAC using Casbin
- **Password Hashing:** bcrypt

### 🔹 Background Processing
- **Async Jobs:** Asynq (Redis-based background job processing)

### 🔹 Validation & Utilities
- **Request Validation:** go-playground/validator
- **Configuration Management:** godotenv

### 🔹 Observability & Monitoring
- **Logging:** Zap (structured logging)
- **Metrics:** Prometheus
- **Visualization:** Grafana

### 🔹 Database Migrations
- **Migration Tool:** golang-migrate

---

## 📁 Project Structure

```text
pharmaciano-backend-go/
│
├── cmd/
│   └── server/              # Application entry point
│
├── internal/
│   ├── config/              # Environment & configuration
│   ├── database/            # PostgreSQL & Redis connections
│   ├── models/              # Database models
│   ├── repository/          # Data access layer
│   ├── services/            # Business logic layer
│   ├── handlers/            # HTTP handlers (controllers)
│   ├── routes/              # API routes
│   ├── auth/                # JWT authentication logic
│   ├── rbac/                # Casbin RBAC setup
│   ├── middlewares/         # Auth, RBAC, logging middleware
│   ├── jobs/                # Async background jobs
│   └── logger/              # Zap logger configuration
│
├── migrations/              # Database migration files
├── .env.example             # Environment variable template
├── go.mod
├── go.sum
└── README.md
