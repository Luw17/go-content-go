# 🚀 Go Content Go!

> An ultra-fast, single-binary CMS powered by Go and SQLite. Built for high performance, zero-setup deployments, and seamless hybrid support (Monolith & Headless).

[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/go-%3E%3D1.22-blue)](https://golang.org/)

**Go Content Go!** é um CMS moderno desenvolvido para ser a alternativa leve, segura e instantânea ao WordPress. Esqueça o overhead de servidores PHP, bancos de dados complexos e centenas de plugins inchados: aqui tudo roda em um único binário compilado.

## Docker

O jeito mais simples de iniciar o CMS localmente é com Docker Compose:

```sh
docker compose up -d --build
```

Abra `http://localhost:8080/admin/setup` para criar a conta de administrador.
Para usar outra porta no host, defina `GO_CONTENT_GO_PORT`, por exemplo
`GO_CONTENT_GO_PORT=9000 docker compose up -d --build`.

O Compose mantém o banco, uploads, backups, temas e plugins em volumes
persistentes nomeados. Os temas `default` e `editorial` vêm na imagem e também
estão embutidos no binário, permitindo executar o CMS sem distribuir uma pasta
de temas separada. Temas locais adicionais e plugins Alpha podem ser instalados
em **Aparência → Instalar tema** e **Plugins → Instalar plugin**, ou colocados
nos respectivos volumes em
`/app/themes/<id>` e `/app/plugins/<id>`.

O painel aceita pacotes ZIP de até 20 MB. Temas devem conter `index.html`,
`single.html`, `page.html`, `404.html` e `style.css`; `theme.json` é opcional.
Plugins devem conter um `plugin.json` válido e o script Lua declarado no
manifesto. Plugins instalados ficam inativos até serem ativados no painel.
Arquivos ZIP são extraídos em staging, validados antes da publicação e
rejeitados se contiverem caminhos de traversal, links simbólicos ou tipos
especiais.

## Segurança e limites

O painel limita tentativas de login a cinco por minuto por endereço IP e
bloqueia novas tentativas por 15 minutos após cinco falhas. Uploads de mídia,
temas e plugins compartilham um limite de 10 requisições por minuto e por IP.
A API REST aceita 60 requisições por minuto por IP por padrão; ajuste com
`GO_CONTENT_GO_API_RATE_LIMIT` (de 1 a 10000) ou com a flag
`-api-rate-limit`. Os limites são mantidos em memória por processo.

O painel também aplica tokens CSRF em todos os métodos que alteram dados,
cookies `HttpOnly` e `SameSite=Strict`, rotação de sessão após autenticação,
senhas bcrypt com custo 12 (hashes antigos são atualizados no próximo login),
política CSP e cabeçalhos anti-clickjacking/MIME sniffing. O SQLite local usa
permissão `0600`; a pasta de uploads usa `0755`, e arquivos internos não são
servidos por rotas HTTP.

Para publicar no Dokploy, conecte o repositório e use o `Dockerfile` ou
`compose.yaml`. Se configurar volumes pela interface do Dokploy, use os mesmos
caminhos de destino: `/app/data`, `/app/uploads`, `/app/backups`, `/app/themes`
e `/app/plugins`. Não monte um volume vazio por cima de `/app/themes` se a
plataforma não copiar os arquivos existentes na imagem para o volume; nesse
caso, os temas `default` e `editorial` embutidos mantêm o site funcional, mas
CSS e demais assets de temas personalizados precisam existir no volume.

## Configurações do site

Em `/admin/settings`, administradores podem configurar o título, a descrição
curta e o logotipo do site. A biblioteca de mídias fornece as imagens elegíveis
para o logotipo. Também é possível escolher entre listar posts publicados ou
exibir uma página publicada como página inicial, além de personalizar os títulos
de introdução e da seção de posts do tema padrão.

As opções são persistidas na tabela `options` do SQLite. Se a página inicial
configurada deixar de estar disponível, a home volta à lista de posts publicados.
O CMS fornece conteúdo, identidade e menus aos templates; cada tema controla a
estrutura HTML e os estilos das páginas públicas. O visual do painel
administrativo é independente dos temas.

O tema **Editorial** é uma segunda opção pronta para ativação em
`/admin/appearance`, com layout e paleta próprios para comparar com o tema
`default`.

## Menus e mídia no editor

Administradores gerenciam os menus em `/admin/appearance/menus`, escolhendo a
localização principal ou rodapé, adicionando páginas publicadas e links locais
ou HTTP(S), e mudando a ordem dos itens com os controles do painel. O tema
padrão usa `{{render_menu "primary"}}` e `{{render_menu "footer"}}`; páginas
removidas ou não publicadas são omitidas automaticamente.

Nos editores de posts e páginas, **Selecionar imagem destacada** e **Inserir
mídia** abrem a biblioteca de imagens em um modal HTMX. A imagem destacada é
pré-visualizada e validada contra a biblioteca ao salvar; a inserção inline
coloca Markdown de imagem no cursor atual.

---

## ⚡ Principais Destaques

- **Binário Único:** Instalação e execução instantâneas. Copiou, rodou, funcionou. Sem dependências externas de Node.js, Python ou Nginx/Apache.
- **SQLite Embarcado:** Zero configuração inicial de banco de dados. Tudo é armazenado de forma eficiente em um único arquivo local.
- **Arquitetura Híbrida:** Use como monólito tradicional (SSR com Go `html/template`) ou como Headless CMS puro consumindo a API REST nativa.
- **Fim da Cascata de CSS:** O core do CMS jamais injeta estilos pesados ou scripts no seu site. O frontend é 100% limpo e controlado pelo seu tema.
- **Ultra Leve:** Consumo mínimo de RAM (na casa de poucos megabytes) e inicialização em microssegundos.
