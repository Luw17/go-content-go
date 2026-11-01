# 🗺️ Go Content Go! — Project Roadmap

Welcome to the **Go Content Go!** development roadmap. This document outlines our strategic path from an initial MVP (Minimum Viable Product) to a fully-featured, production-ready CMS.

---

## 🎯 Project Vision
**Go Content Go!** is designed to be an ultra-fast, single-binary CMS powered by Go and SQLite. It aims to bridge the gap between monolithic simplicity and headless performance with zero infrastructure friction.

---

## 📌 Phase 1: MVP & Core Engine (v0.1.0)
> **Goal:** Establish a functional single-binary server with SQLite auto-migrations, an administrative interface, and basic page rendering + JSON API.

### 1.1 Foundation & Infrastructure
- [x] Initialize standard Go project architecture (`/cmd`, `/internal`, `/web`).
- [x] Implement CGO-free SQLite driver integration (`modernc.org/sqlite`).
- [x] Auto-migration engine for core tables (`users`, `posts`, `options`).
- [x] Embedded static assets and template system using Go `//go:embed`.
- [x] Configuration manager via environment variables and optional `.env` / flags.

### 1.2 Auth & Initial Setup
- [x] First-run installer wizard (`/admin/setup`) for initial superadmin creation.
- [x] Secure session-based authentication using cookies.
- [x] Password hashing using `bcrypt`.
- [x] Admin login/logout flows.

### 1.3 Content Management (CRUD)
- [x] Admin dashboard UI (Go templates, one embedded stylesheet, and locally bundled HTMX).
- [x] Post/Page creation and editing (Title, Slug, Content, Status: Draft/Published).
- [x] Basic Markdown input with sanitized HTML rendering and live preview.
- [x] Post deletion and status toggling, including trash, restore, and permanent deletion.
- [x] Categories (hierarchical), tags, and featured image fields for posts.

### 1.4 Rendering & Headless API
- [x] Public SSR routes (`/` and `/{slug}`) using Go native HTML templates.
- [x] REST API endpoints:
  - `GET /api/v1/posts` (Paginated list of published posts).
  - `GET /api/v1/posts/{slug}` (Single post detail).
  - `GET /api/v1/pages/{slug}` (Single page detail).
- [x] CORS middleware for headless consumers.

---

## 🚀 Phase 2: Media, Extensibility & UI (v0.2.0)
> **Goal:** Improve content editing capabilities, asset management, and introduce initial theme/plugin interfaces.

### 2.1 Media & File Management
- [x] Local file upload system (`/uploads` directory management).
- [x] Media gallery UI inside the admin dashboard.
- [x] Image metadata indexing and MIME validation.

### 2.2 Theme System
- [x] Dynamic layout rendering engine allowing custom HTML template folders.
- [x] Theme switcher in the admin settings.

### 2.3 Plugin Architecture (Research & Alpha)
- [x] Event hook system (`before_save`, `after_save`, `render_head`).
- [x] Alpha Lua runtime with activation management, restricted libraries, and hook timeouts.
- [ ] Exploration of WebAssembly (WASM via `wazero`) runtime for stronger third-party isolation.

---

## 🛡️ Phase 3: Enterprise Readiness & Performance (v0.3.0+)
> **Goal:** High concurrency optimizations, caching layers, and security hardening.

- [x] In-memory TTL/LRU response caching for public SSR and API routes, ETags, and mutation invalidation.
- [x] Database optimization with `PRAGMA optimize` and `VACUUM`.
- [x] Role-Based Access Control (RBAC) (Admin, Editor, Author), including ownership checks for authors.
- [x] Online SQLite hot backup, scheduled local backups, and administrator download/maintenance controls.
- [x] Docker image and CGO-free builds for Linux, macOS, and Windows.
- [x] Rate limiting on login and public API endpoints.

Run `make build` to generate the five platform binaries under `dist/`. Build the minimal container with `docker build -t go-content-go .`; mount persistent volumes for `/app/data`, `/app/uploads`, and `/app/backups`.

---

## 📜 License
This project is licensed under the **Apache License 2.0**. See the [LICENSE](LICENSE) file for full details.