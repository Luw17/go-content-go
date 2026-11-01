# 🚀 Go Content Go!

> An ultra-fast, single-binary CMS powered by Go and SQLite. Built for high performance, zero-setup deployments, and seamless hybrid support (Monolith & Headless).

[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/go-%3E%3D1.22-blue)](https://golang.org/)

**Go Content Go!** é um CMS moderno desenvolvido para ser a alternativa leve, segura e instantânea ao WordPress. Esqueça o overhead de servidores PHP, bancos de dados complexos e centenas de plugins inchados: aqui tudo roda em um único binário compilado.

---

## ⚡ Principais Destaques

- **Binário Único:** Instalação e execução instantâneas. Copiou, rodou, funcionou. Sem dependências externas de Node.js, Python ou Nginx/Apache.
- **SQLite Embarcado:** Zero configuração inicial de banco de dados. Tudo é armazenado de forma eficiente em um único arquivo local.
- **Arquitetura Híbrida:** Use como monólito tradicional (SSR com Go `html/template`) ou como Headless CMS puro consumindo a API REST nativa.
- **Fim da Cascata de CSS:** O core do CMS jamais injeta estilos pesados ou scripts no seu site. O frontend é 100% limpo e controlado pelo seu tema.
- **Ultra Leve:** Consumo mínimo de RAM (na casa de poucos megabytes) e inicialização em microssegundos.
