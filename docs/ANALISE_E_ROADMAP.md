# Análise Técnica, de Produto e Roadmap — Go Content Go!

Análise completa do projeto `go-content-go` cobrindo proposta de valor, SEO/indexação, segurança, interfaces (admin e site público), arquitetura/API Headless e roadmap priorizado.

---

## 1. Proposta de Valor e Posicionamento

O **Go Content Go!** já entrega uma base técnica sólida e diferenciada:
- **Binário único CGO-free (`scratch` Docker image)** com SQLite em WAL, zero dependências externas e inicialização instantânea.
- **Arquitetura híbrida real**: SSR limpo (temas `default` e `editorial` + Page Builder visual + Header/Footer globais) + API REST Headless (`/api/v1/`).
- **Motor de extensibilidade seguro**: instalação de temas e plugins via ZIP (com validação anti-traversal/symlink) e runtime Lua com timeout.
- **Base sólida de segurança**: CSRF global, bcrypt custo 12, 2FA (TOTP), CSP estrito, sanitização `bluemonday` e testes de carga (14/14 pacotes de teste passando).

### Gaps que hoje limitam adoção e resultado em produção
1. **Descoberta orgânica incompleta (SEO técnico)**: o pacote `internal/seo/seo.go` já gera metadados on-page, mas faltam os arquivos de rastreamento que o Google exige (`/sitemap.xml`, `/robots.txt`, feed RSS) e estrutura de linkagem interna (paginação e arquivos de categoria/tag).
2. **Prontidão atrás de Proxy Reverso**: embora o deploy recomendado use Docker/Dokploy, cookies `Secure` e rate-limiting por IP ainda assumem conexão direta sem proxy.
3. **Lacunas de operação no dia a dia**: gestão de usuários incompleta (não edita/troca senha/exclui), dashboard sem indicadores, backups sem política de retenção e API Headless ainda muito enxuta.

---

## 2. Diagnóstico de SEO e Indexação no Google (Prioridade Máxima)

Embora `internal/seo/seo.go` já gere `<link rel="canonical">`, `meta robots`, Open Graph, Twitter Cards e JSON-LD (`WebSite`, `Organization`, `BlogPosting`, `BreadcrumbList`), faltam peças críticas de rastreamento e descoberta no servidor HTTP:

| Problema identificado | Onde está no código | Impacto no Google / SEO |
| :--- | :--- | :--- |
| **Sem `/sitemap.xml`** | Ausente em `internal/web/server.go` — cai no fallback `/` e retorna `404`. | Google Search Console não descobre URLs automaticamente nem monitora cobertura. |
| **Sem `/robots.txt`** | Ausente em `internal/web/server.go` — retorna `404`. | Crawlers não recebem `Sitemap: https://.../sitemap.xml` nem o bloqueio de `/admin/`. |
| **`HEAD` retorna `405`** | `internal/web/server.go` aceita apenas `GET` nas rotas públicas. | Crawlers, monitores de uptime e verificadores de link que usam `HEAD` recebem `405 Method Not Allowed`. |
| **Home limitada a 20 posts sem paginação** | `content.Published(..., 20)` em `internal/web/content_handler.go`. | Posts além dos 20 mais recentes viram **páginas órfãs** (sem links internos). |
| **Sem páginas de Categoria/Tag** | Categorias e tags aparecem como texto plano em `themes/default/single.html`. | Perda de páginas de arquivo (`/categoria/{slug}`, `/tag/{slug}`) para linkagem interna e cauda longa. |
| **Sem Feed RSS/Atom** | Não há `/rss.xml` nem `<link rel="alternate" type="application/rss+xml">`. | Reduz descoberta por agregadores, leitores e bots de indexação rápida. |
| **Sem Redirects 301** | Alterar o slug de um post/página quebra a URL antiga imediatamente. | Perda de autoridade/indexação e erros 404 no Search Console. |
| **`SiteURL` opcional sem alerta** | `internal/seo/seo.go` faz fallback para `r.Host`. | Sem `site_url` preenchido em `/admin/settings`, o canonical pode variar entre `http`/`https` ou `www`/sem `www`. |
| **Sem `/favicon.ico` configurável** | Nenhuma rota ou campo de favicon em `internal/siteconfig/`. | Erros 404 constantes nos logs e aparência genérica na SERP do Google. |

### Checklist operacional fora do código (após subir sitemap + robots.txt)
1. Conferir em `/admin/settings` se `URL do site` (`site_url`) está preenchida com `https://seudominio.com` e se **Indexação** não está em `noindex`.
2. Verificar a propriedade no **Google Search Console** e enviar `https://seudominio.com/sitemap.xml`.
3. Usar **Inspeção de URL** na home e nos principais posts para solicitar indexação manual imediata.

---

## 3. Análise de Segurança

### Pontos fortes já consolidados
- Proteção CSRF global em mutações `/admin/*` com `ConstantTimeCompare` (`internal/web/security.go`).
- `bcrypt` custo 12 com upgrade transparente de hashes antigos e `dummyPasswordHash` contra enumeração por timing (`internal/auth/auth.go`).
- SVG servido com `Content-Security-Policy: default-src 'none'; sandbox` (`internal/web/media_handler.go`).
- Bloqueio de arquivos/diretórios internos (`.git`, `.db`, `.db-wal`, `/data`, `/backups`, `/plugins`) em `internal/web/security.go`.
- Validação de pacotes ZIP em staging contra path traversal e symlinks (`internal/packagezip/install.go`).

### Riscos e vulnerabilidades operacionais para corrigir
1. **Quebra de segurança atrás de Proxy Reverso (Docker / Dokploy / Traefik / Nginx / Cloudflare)**:
   - **Cookies sem flag `Secure`**: em `internal/web/server.go`, os cookies `gocg_session` e `gocg_csrf` usam `Secure: r.TLS != nil`. Atrás de proxy HTTPS, `r.TLS` é `nil` e os cookies trafegam sem `Secure`.
   - **Rate limit e bloqueio de login compartilhados**: `clientAddress(r)` em `internal/web/server.go` usa apenas `r.RemoteAddr`. Atrás de proxy, todos os visitantes compartilham o IP do proxy — 5 falhas de login bloqueiam o admin para todos (DoS) e o rate limit da API/formulários vira global.
   - **Solução**: adicionar configuração explícita `GO_CONTENT_GO_TRUSTED_PROXIES` (CIDRs confiáveis) para extrair o IP real de `X-Forwarded-For` e detectar HTTPS via `X-Forwarded-Proto` somente de proxies confiáveis.
2. **Fluxo de 2FA (TOTP)**:
   - Em `internal/web/server.go`, enviar usuário/senha sem `totp_code` na etapa 1 chama `h.loginProtection.Failed(ip, ...)`, penalizando logins legítimos.
   - Sem **códigos de recuperação (recovery codes)** nem reset administrativo de 2FA.
   - `internal/auth/totp.go` aceita reutilização do mesmo código na janela de 90s (sem controle do último step usado).
   - `totp_secret` armazenado em texto puro no SQLite.
3. **Ciclo de vida de usuários incompleto**:
   - `internal/web/enterprise_handler.go` só permite criar e listar usuários. Faltam troca de senha, alteração de papel (RBAC), desativação/exclusão e revogação de sessões ativas.
4. **Risco de esgotamento de disco em Backups**:
   - `GET /admin/settings/backup/download` (`internal/web/enterprise_handler.go`) gera um novo arquivo em `backups/` a cada download e `RunScheduledBackups` (`internal/database/backup.go`) nunca remove backups antigos. Falta política de retenção.
5. **Cabeçalho HSTS**: ausente quando servido em HTTPS atrás de proxy confiável (`Strict-Transport-Security`).

---

## 4. Análise de Interfaces (UX / UI)

### Painel Administrativo (`/admin/`)
- **Pontos fortes**: interface limpa e rápida com HTMX local, preview Markdown ao vivo, prévia de SERP do Google + contadores SEO (`web/templates/admin.html`), Page Builder visual com presets.
- **Melhorias necessárias**:
  1. **Dashboard vivo**: hoje exibe apenas um card estático de boas-vindas. Adicionar KPIs (posts publicados/rascunhos, páginas, mídias, envios recentes de formulários) e **Checklist de Saúde SEO & Segurança** (alerta se `site_url` vazia, `noindex` ativo, 2FA desativado ou último backup).
  2. **Sidebar adaptada ao RBAC**: `web/templates/admin.html` exibe links exclusivos de `admin` para `author` e `editor`, resultando em `403`.
  3. **Editor de conteúdo**: toolbar Markdown, autosave local, agendamento de publicação (`scheduled_at`), histórico de revisões, busca/filtros e paginação nas listagens do admin.
  4. **Biblioteca de Mídias**: edição de `alt_text`/descrição após upload, busca/filtro e geração de thumbnails/WebP.
  5. **Formulários do Builder**: exportação CSV e notificação por e-mail/webhook.

### Site Público (Frontend & Temas)
- **Pontos fortes**: HTML semântico, zero CSS/JS injetado pelo core, `loading="lazy"` em thumbnails, `fetchpriority="high"` na capa, suporte a `color-scheme: light dark`.
- **Melhorias necessárias**:
  1. Paginação na Home (`/page/2` ou `?page=2`) com `rel="prev"`/`rel="next"`.
  2. Páginas de arquivo de Categoria e Tag clicáveis nos posts.
  3. Busca pública (`/search`) com SQLite FTS5.
  4. Cache-busting (`?v=hash`) nos assets de temas (`/themes/{theme}/style.css` hoje usa `no-cache`).
  5. Posts relacionados e tempo estimado de leitura.

---

## 5. Arquitetura, Headless API e Operação

1. **API Headless (`/api/v1/`) muito enxuta**: `apiContent` (`internal/web/api_handler.go`) só retorna `id`, `title`, `slug`, `content`, `status`, `published_at`. Faltam `content_html`, `summary`, `featured_image`, `author`, `categories`, `tags`, bloco `seo` e endpoints `/api/v1/pages`, `/api/v1/categories`, `/api/v1/menus/{location}` e `/api/v1/site`.
2. **Compressão HTTP**: adicionar middleware opcional de `gzip` para HTML/CSS/JSON.
3. **Observabilidade e limpeza**: padronizar logs em `slog`, remover código morto (`adminPlaceholder` em `internal/web/content_handler.go`) e criar fluxo de listagem/restauração de backups.

---

## 6. Roadmap Priorizado (v2.0.1 → v2.3+)

### Onda 0 — Correções Críticas de Produção & Indexação (v2.0.1 — Imediato)
> **Objetivo:** destravar a indexação no Google e fechar riscos de deploy atrás de proxy reverso.

- [x] **Gerar `/sitemap.xml` dinâmico e cacheado**: incluir Home, páginas publicadas e posts publicados (respeitando `noindex = false` e `SEORobotsIndexing`), usando `seo.BaseURL` e `<lastmod>` em RFC3339.
- [x] **Gerar `/robots.txt` dinâmico**: permitir rastreamento público (ou `Disallow: /` se `noindex`), bloquear `/admin/` e `/api/`, e apontar `Sitemap: {BaseURL}/sitemap.xml`.
- [x] **Suportar método `HEAD`** nas rotas públicas (`/`, `/{slug}`, `/sitemap.xml`, `/robots.txt`).
- [x] **Suporte seguro a Proxy Reverso (`GO_CONTENT_GO_TRUSTED_PROXIES`)**: corrigir `clientAddress(r)` e flag `Secure` dos cookies via `X-Forwarded-For` / `X-Forwarded-Proto` apenas para proxies confiáveis.
- [x] **Corrigir 2FA**: não penalizar etapa 1 sem código no cooldown, impedir replay do mesmo código TOTP e adicionar códigos de recuperação (recovery codes).
- [x] **Política de retenção de backups** (manter últimos N backups automáticos e evitar acúmulo infinito nos downloads).

### Onda 1 — Crescimento Orgânico & Descoberta de Conteúdo (v2.1)
> **Objetivo:** transformar o site público em uma máquina de SEO e retenção de leitores.

- [x] **Paginação na Home** com `rel="prev"`/`rel="next"`, removendo o limite fixo de 20 posts.
- [x] **Páginas de arquivo de Categorias (`/categoria/{slug}`) e Tags (`/tag/{slug}`)** com SEO próprio, breadcrumbs e inclusão no `sitemap.xml`.
- [x] **Feed RSS 2.0 (`/rss.xml`)** com autodiscovery `<link rel="alternate">` no `<head>`.
- [x] **Gerenciador de Redirecionamentos 301/302** no admin (com criação automática opcional ao mudar slug de conteúdo publicado).
- [x] **Favicon / Apple Touch Icon** configurável em `/admin/settings` + rota `/favicon.ico`.
- [x] **Busca pública (`/search`)** com SQLite FTS5.
- [x] **Posts relacionados** e **tempo de leitura** nos temas `default` e `editorial`.

### Onda 2 — Experiência Editorial, Gestão e Conversão (v2.2)
> **Objetivo:** melhorar a produtividade diária de quem opera o CMS.

- [x] **Dashboard vivo**: KPIs de conteúdo/mídia/formulários + Checklist de Saúde SEO & Segurança.
- [x] **Gestão completa de Usuários**: trocar senha, editar papel, desativar/excluir usuário e revogar sessões ativas.
- [x] **Sidebar adaptada ao papel (RBAC)** para `editor` e `author`.
- [x] **Agendamento de publicação** (`scheduled_at`) com publicação automática e invalidação de cache/sitemap.
- [x] **Histórico de revisões** de posts/páginas com restauração em 1 clique.
- [x] **Melhorias no Editor**: toolbar Markdown, autosave local, busca/filtros/paginação nas tabelas do admin.
- [x] **Evolução dos Formulários do Builder**: notificação via Webhook/SMTP e exportação em CSV.

### Onda 3 — Headless v2, Mídia Moderna e Ecossistema (v2.3+)
> **Objetivo:** consolidar a proposta híbrida e a diferenciação frente a WordPress/Ghost.

- [x] **API REST enriquecida**: retornar HTML + Markdown, `featured_image`, `author`, `categories`, `tags`, `seo`, além de `/pages`, `/categories`, `/menus/{location}`, `/site` e API keys para preview de rascunhos.
- [x] **Pipeline de Mídia**: edição de `alt_text`/descrição, geração de variantes responsivas (thumbnails / WebP) e suporte opcional a storage S3/R2.
- [x] **Performance de rede**: middleware opcional de compressão `gzip` + cache-busting (`?v=hash`) nos assets de temas.
- [x] **Importador / Exportador**: importar Markdown/WordPress WXR e exportar todo o conteúdo em JSON/Markdown.
- [x] **Observabilidade & Limpeza**: padronizar logs em `slog`, remover código morto (`adminPlaceholder`) e avaliar runtime WASM (`wazero`) para plugins de terceiros.
