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
persistentes nomeados. O tema `default` vem na imagem e também está embutido no
binário como fallback. Temas adicionais e plugins Alpha podem ser colocados nos
respectivos volumes em `/app/themes/<id>` e `/app/plugins/<id>` e permanecem
após recriações do container. A instalação de plugins/temas pela interface
administrativa ainda não está disponível; por enquanto, adicione-os ao volume
por um processo de deploy ou administração do servidor.

Para publicar no Dokploy, conecte o repositório e use o `Dockerfile` ou
`compose.yaml`. Se configurar volumes pela interface do Dokploy, use os mesmos
caminhos de destino: `/app/data`, `/app/uploads`, `/app/backups`, `/app/themes`
e `/app/plugins`. Não monte um volume vazio por cima de `/app/themes` se a
plataforma não copiar os arquivos existentes na imagem para o volume; nesse
caso, o fallback embutido mantém o tema padrão funcional, mas o CSS e os demais
assets personalizados precisam existir no volume.

---

## ⚡ Principais Destaques

- **Binário Único:** Instalação e execução instantâneas. Copiou, rodou, funcionou. Sem dependências externas de Node.js, Python ou Nginx/Apache.
- **SQLite Embarcado:** Zero configuração inicial de banco de dados. Tudo é armazenado de forma eficiente em um único arquivo local.
- **Arquitetura Híbrida:** Use como monólito tradicional (SSR com Go `html/template`) ou como Headless CMS puro consumindo a API REST nativa.
- **Fim da Cascata de CSS:** O core do CMS jamais injeta estilos pesados ou scripts no seu site. O frontend é 100% limpo e controlado pelo seu tema.
- **Ultra Leve:** Consumo mínimo de RAM (na casa de poucos megabytes) e inicialização em microssegundos.
