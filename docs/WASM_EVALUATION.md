# Avaliação Técnica: Runtime WASM (`wazero`) para Plugins no Go Content Go!

## Contexto
Atualmente o Go Content Go! executa plugins de terceiros via **Lua embarcado (`gopher-lua`)** com sandbox de bibliotecas restritas (`base`, `table`, `string`, `math`) e timeout rígido por hook (`plugins.DefaultTimeout`).

## Comparativo: Lua (`gopher-lua`) vs WebAssembly (`wazero`)

| Critério | Lua (`gopher-lua` — atual) | WebAssembly (`wazero` — CGO-free) |
| :--- | :--- | :--- |
| **Binário único CGO-free** | Sim (~1 MB) | Sim (~4-6 MB adicionais com compilador nativo) |
| **Isolamento de memória** | Compartilha heap Go; protegido por ausência de `os`/`io` | Memória linear isolada por módulo com limite configurável |
| **Experiência para autores de plugins** | Um único arquivo `main.lua` editável em texto puro, sem etapa de build | Requer toolchain (Rust, TinyGo, AssemblyScript) para compilar `.wasm` |
| **Latência por hook** | Microssegundos (interpretador leve) | Baixa com módulos pré-compilados, maior custo de serialização JSON/ABI nos hostcalls |
| **Segurança contra CPU/RAM abusivos** | Timeout via `context.Context` | Timeout via `WithCloseOnContextDone(true)` + teto rígido de páginas de memória |

## Recomendação
- **Manter Lua como runtime padrão** para plugins leves de conteúdo/SEO (zero fricção para desenvolvedores e hospedagem minimalista).
- **Introduzir `wazero` como runtime opcional (v3.0+)** quando houver demanda por plugins de terceiros não confiáveis ou escritos em Rust/TinyGo, reaproveitando a mesma interface de hooks (`action` / `filter`) sobre uma ABI JSON estável (`plugin.json` com `"runtime": "wasm"`).
