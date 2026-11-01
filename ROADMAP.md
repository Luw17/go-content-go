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



🗺️ Go Content Go! (GCG) — Roadmap Estratégico Pós-v1.0

Este roadmap detalha as fases de desenvolvimento focadas em transformar o core robusto do GCG v1.0 numa plataforma de gerenciamento de conteúdo completa, priorizando a experiência do usuário e a customização visual.
🏗️ Fase 4: Configurações Globais e Identidade

    Goal: Dar controle total ao administrador sobre a identidade do site e configurações básicas do core.

4.1 Identidade do Site (Ref. Terceiro Ponto)

    [x] Implementar seção em /admin/settings: "Identidade do Site".

    [x] Campos SQLite para: site_title (Título do Site), site_tagline (Descrição Curta/Slogan), site_logo (Upload de logo via Media).

    [x] Atualizar o motor de renderização Go e o tema padrão para utilizar essas variáveis dinâmicas em vez de textos hardcoded.

4.2 Configurações da Home (Ref. Segundo Ponto)

    [x] Implementar seção em /admin/settings: "Configurações de Leitura".

    [x] Criar opção para definir o que exibir na home:

        posts (Lista cronológica reversa, padrão v1.0).

        page (Uma página estática específica selecionada no banco).

    [x] Adicionar campos extras de customização leve para a Home (ex: "Título da seção de posts", "Título de introdução").

🎨 Fase 5: Estrutura Visual e Navegação

    Goal: Resolver a navegação dinâmica do site e aprimorar a inserção de mídia no conteúdo.

5.1 Sistema de Menus Dinâmicos (Ref. Primeiro Ponto)

    [x] Database Structure: Criar tabelas SQLite menus e menu_items com localização, tipo, referência de página, ordem e parent_id.

    [x] Admin UI: Criar gerenciador em /admin/appearance/menus para adicionar páginas e links e reordenar itens; arrastar e soltar/hierarquia ficam para uma evolução futura.

    [x] Configuração: Permitir mapear um menu criado para uma localização de tema (Menu Principal ou Rodapé).

    [x] Theme Integration: Expor render_menu nos templates Go e renderizar os menus configurados no tema padrão.

5.2 Gerenciador de Mídia Integrado (Ref. Quarto e Sexto Pontos)

    [x] Media Picker: Implementar modal via HTMX para selecionar imagens da Biblioteca de Mídias no editor de Post/Página.

    [x] Featured Image Flow: Selecionar imagem no picker, exibir prévia e validar o caminho indexado antes de salvar.

    [x] Inline Media Flow: Inserir Markdown de imagem no ponto atual do cursor no editor.

🔌 Fase 6: Gerenciamento de Extensibilidade

    Goal: Facilitar a instalação e gerenciamento de temas e plugins via painel administrativo.

6.1 Instalador de Temas (Ref. Quinto Ponto)

    [x] Implementar view em /admin/appearance/install.

    [x] Permitir upload de arquivos .zip contendo o tema, com limite de 20 MB.

    [x] Extrair em staging e validar os templates obrigatórios, os metadados opcionais de theme.json e os caminhos para prevenir Path Traversal antes de publicar em /themes/{nome-do-tema}/.

6.2 Instalador de Plugins (Ref. Quinto Ponto) (v1.2.0)

    [x] Implementar view em /admin/plugins/install.

    [x] Permitir upload de arquivos .zip contendo o plugin, com limite de 20 MB.

    [x] Extrair em staging e validar plugin.json, o script Lua e os caminhos antes de publicar em /plugins/{nome-do-plugin}/.

    [x] Atualizar a lista administrativa após a instalação; plugins instalados ficam inativos até serem ativados no painel.

🛡️ Fase 7: Segurança e Hardening

    Goal: Preparar o GCG para ameaças de segurança complexas e ambientes corporativos.

    [x] Limitar login a cinco tentativas por minuto por IP e aplicar cooldown de 15 minutos após cinco falhas.

    [x] Aplicar rate limit por IP aos uploads de mídia, temas e plugins; permitir configurar o limite da API REST.

    [x] Aplicar proteção CSRF global aos métodos mutáveis do painel e cabeçalhos HTTP CSP, X-Frame-Options, nosniff e Referrer-Policy.

    [x] Reforçar cookies de sessão, rotacionar o ID de sessão após autenticação e atualizar hashes bcrypt antigos para custo 12.

    [x] Sanitizar HTML dos editores antes de persistir e manter a sanitização do HTML renderizado.

    [x] Restringir permissões do SQLite e uploads e bloquear rotas de arquivos/diretórios internos.

    [ ] Implementar autenticação de dois fatores (2FA) nativa.

    [ ] Completar auditoria de permissões de arquivo em todos os diretórios e plataformas suportados.

🏆 Fase 8: GCG v2.0 Release Candidate

    Goal: Consolidar todas as features, realizar testes de integração e lançar a versão 2.0.

    [ ] Testes de carga simulando 10.000 posts e 1.000 usuários simultâneos com cache de memória ativo.

    [ ] Validação da documentação final de temas e plugins.

    [ ] Lançamento da versão 2.0.0 estável.