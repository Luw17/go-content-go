# Guia de Temas, Construtor Visual e Plugins — Go Content Go! v2.0

Este guia documenta como criar temas personalizados, utilizar o Construtor Visual e desenvolver plugins Lua seguros no **Go Content Go!**.

---

## 1. Desenvolvimento de Temas

Um tema válido é um diretório em `themes/<id>/` (ou um arquivo `.zip` enviado em `/admin/appearance/install`) contendo os arquivos obrigatórios:

- `index.html` — Homepage do site (suporta `.BuilderHTML` quando há uma Homepage visual ativa, ou `.Posts` no modo clássico).
- `single.html` — Visualização de um post publicado (`.Item`).
- `page.html` — Visualização de uma página publicada (`.Item` e `.BuilderHTML` opcional).
- `404.html` — Página de erro 404.
- `style.css` — Folha de estilo principal do tema.
- `theme.json` *(opcional)* — Metadados `name`, `author` e `version`.

### Funções disponíveis nos templates Go

| Função | Descrição |
|--------|-----------|
| `{{render_menu "primary"}}` / `{{render_menu "footer"}}` | Renderiza os menus cadastrados em `/admin/appearance/menus`. |
| `{{render_header}}` | Renderiza o cabeçalho visual configurado em `/admin/appearance/sections` de acordo com o escopo (`slug` > `home`/`posts`/`pages` > `all`). Retorna vazio se nenhum cabeçalho customizado estiver ativo. |
| `{{render_footer}}` | Renderiza o rodapé visual configurado em `/admin/appearance/sections` de acordo com o escopo. |

---

## 2. Construtor Visual e Templates Globais

### Layouts Modulares (`/admin/builder`)
- Crie layouts reutilizáveis a partir do zero ou usando modelos prontos (*Homepage Editorial*, *Landing Page de Produto*, *Página Institucional*).
- Componentes disponíveis: **Hero Section**, **Lista de Posts**, **Grade de Destaques**, **Formulário** (Contato ou Newsletter, persistido em `form_submissions` via `POST /forms/submit`), **Texto / Markdown** e **Banner CTA**.
- Qualquer layout pode ser ativado como **Homepage** ou associado a páginas individuais no campo **Layout Visual (Template)** em `/admin/pages/{id}/edit`.

### Cabeçalhos e Rodapés Globais (`/admin/appearance/sections`)
- Permite configurar diferentes cabeçalhos e rodapés visuais para **Todo o site**, **Somente Homepage**, **Artigos**, **Páginas** ou **Slug específico**.
- Precedência automática: `slug específico` > `escopo da seção` > `todo o site` > padrão do tema.

---

## 3. Desenvolvimento de Plugins Lua

Um plugin reside em `plugins/<id>/` (ou é instalado via `.zip` em `/admin/plugins/install`) com `plugin.json` e o arquivo principal `.lua`.

### Manifesto (`plugin.json`)

```json
{
  "id": "meu-plugin",
  "name": "Meu Plugin",
  "version": "1.0.0",
  "description": "Descrição breve",
  "author": "Seu Nome",
  "main_file": "main.lua",
  "hooks": [
    "content:before_render",
    "content:after_render",
    "head:inject_jsonld",
    "admin:menu_register",
    "admin:page_register"
  ]
}
```

### Hooks suportados

| Hook | Tipo | Descrição |
|------|------|-----------|
| `post:before_save` | Ação | Disparado antes de salvar um post/página. |
| `post:after_save` | Ação | Disparado após salvar um post/página. |
| `user:login` | Ação | Disparado após autenticação bem-sucedida. |
| `content:before_render` | Filtro | Transforma o Markdown antes da renderização. |
| `content:render` | Filtro | Transforma o Markdown (compatibilidade v1.x). |
| `content:after_render` | Filtro | Transforma o HTML após a renderização (re-sanitizado via `bluemonday`). |
| `head:inject_tags` | Filtro | Injeta tags `<meta>` e `<link>` sanitizadas no `<head>` (recebe `pageContext`). |
| `head:inject_jsonld` | Filtro | Retorna uma string JSON-LD válida; validada e inserida como `<script type="application/ld+json">` (recebe `pageContext`). |
| `admin:menu_register` | Consulta | Registra um item na barra lateral administrativa apontando para `/admin/plugin/<id>` (`return { label = "..." }`). |
| `admin:page_register` | Consulta | Renderiza conteúdo sanitizado na página administrativa `/admin/plugin/<id>` (`return { title = "...", html = "..." }`). |

### API de Settings Isoladas (`plugin_settings`)

Cada plugin tem acesso à tabela global `settings` no sandbox Lua, isolada estritamente pelo seu próprio `plugin_id`:

```lua
local valor = settings.get("minha_chave")
settings.set("minha_chave", "novo_valor")
local todas = settings.all()
settings.delete("minha_chave")
```

As configurações também podem ser visualizadas e editadas diretamente pelo administrador em `/admin/plugin/<id>`.

### Isolamento e Segurança
- Bibliotecas perigosas (`os`, `io`, `dofile`, `loadfile`, `load`, `collectgarbage`) são bloqueadas no sandbox Lua.
- Todo hook executa sob timeout configurável (padrão: `100ms`) com recuperação de pânicos.
- Todos os retornos HTML e JSON-LD passam por sanitização/validação estrita antes da entrega ao navegador.

---

## 4. Segurança e 2FA (TOTP)
- Qualquer usuário autenticado pode ativar ou desativar **2FA (RFC 6238 TOTP)** em `/admin/security`.
- Em `/admin/settings`, o painel exibe a **Auditoria de Permissões de Arquivos** verificando os diretórios de uploads, temas, plugins e backups.
