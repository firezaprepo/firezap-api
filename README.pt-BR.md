<div align="center">

# FireZAP API

**API de WhatsApp open source com painel próprio — instâncias, grupos, webhooks e mais.**

[English](README.md) · **Português (Brasil)**

![status](https://img.shields.io/badge/status-em%20desenvolvimento-orange)
![licença](https://img.shields.io/badge/licen%C3%A7a-Apache%202.0-blue)
![go](https://img.shields.io/badge/Go-1.23+-00ADD8)

</div>

> [!WARNING]
> **Este projeto está no começo do desenvolvimento.** Os recursos abaixo descrevem o plano das
> primeiras versões — ainda não estão disponíveis. Acompanhe o [roadmap](#roadmap) e marque
> "Watch" no repositório para saber da primeira versão.

---

## Sumário

- [O que é a FireZAP API](#o-que-é-a-firezap-api)
- [Recursos](#recursos)
- [Arquitetura](#arquitetura)
- [Início rápido](#início-rápido)
- [Configuração](#configuração)
- [Visão geral da API](#visão-geral-da-api)
- [Webhooks](#webhooks)
- [Painel](#painel)
- [Uso responsável](#uso-responsável)
- [Roadmap](#roadmap)
- [Como contribuir](#como-contribuir)
- [Segurança](#segurança)
- [Licença](#licença)

---

## O que é a FireZAP API

A FireZAP API é uma API HTTP que você hospeda no seu próprio servidor para ligar números de
WhatsApp aos seus sistemas: CRMs, chatbots, help desks, e-commerce e automações. Ela roda na sua
VPS, os dados ficam com você, e tudo é gerenciado por um painel web.

É criada e mantida pelo time do [FireZAP](https://firezap.com.br) e liberada gratuitamente para
a comunidade, sob a licença Apache 2.0.

**Dois modos de conexão, uma API só:**

| Modo | Como conecta | Indicado para |
|---|---|---|
| **Web (QR code / código de pareamento)** | Protocolo multi-dispositivo, como o WhatsApp Web, usando o [whatsmeow](https://github.com/tulir/whatsmeow) | Começar rápido, volumes pequenos e médios |
| **Cloud API (oficial)** | Plataforma oficial WhatsApp Business da Meta | Estabilidade garantida, volume alto, conformidade |

O seu código usa os mesmos endpoints nos dois modos — trocar um número de modo não exige
reescrever a integração.

## Recursos

Planejados para as primeiras versões:

**Instâncias**
- Instâncias ilimitadas (uma por número), cada uma isolada — a falha de uma nunca afeta as outras
- Conexão por QR code ou por código de pareamento (número de telefone)
- Sessão gravada no banco: sobrevive a reinícios sem ler o QR code de novo
- Reconexão automática e monitoramento, com alerta quando uma instância cai

**Mensagens**
- Texto, imagem, vídeo, documento, áudio (inclusive mensagem de voz) e figurinhas
- Reações, respostas (mensagem citada), editar e apagar
- Confirmação de entrega e de leitura
- Fila de envio por instância, com limite de envio configurável

**Grupos**
- Criar grupos, alterar nome, descrição e foto
- Adicionar, remover, promover e rebaixar participantes
- Links de convite: criar, revogar e entrar
- Ler os dados do grupo e a lista de participantes

**Integrações**
- Webhooks para todos os eventos, com novas tentativas e assinatura
- API REST com especificação OpenAPI (Swagger)
- Chave de API por instância e uma chave geral de administrador

**Painel**
- Painel web para gerenciar instâncias, ler QR codes, ver logs e configurar webhooks
- Disponível em **inglês** e **português (Brasil)**

## Arquitetura

```
┌──────────────┐    REST + chave de API   ┌───────────────────────────────┐
│ Seu sistema  │ ───────────────────────▶ │          FireZAP API          │
│ (CRM, bot…)  │ ◀─────────────────────── │  ┌─────────┐   ┌───────────┐  │
└──────────────┘         Webhooks         │  │  Fila   │   │  Painel   │  │
                                          │  └────┬────┘   └───────────┘  │
                                          │       ▼                       │
                                          │  ┌──────────────────────────┐ │
                                          │  │ Provedores               │ │
                                          │  │  • Web (whatsmeow)       │ │
                                          │  │  • Cloud API (Meta)      │ │
                                          │  └──────────────────────────┘ │
                                          └───────────────┬───────────────┘
                                                          ▼
                                                    PostgreSQL
                                     (sessões, instâncias, histórico de envio)
```

- **Linguagem:** Go — pouca memória por instância e um binário único
- **Banco:** PostgreSQL
- **Mídia:** disco local ou qualquer armazenamento compatível com S3
- **Instalação:** imagem Docker e `docker compose`

## Início rápido

> Disponível a partir da primeira versão.

```bash
git clone https://github.com/firezaprepo/firezap-api.git
cd firezap-api
cp .env.example .env        # defina FZ_ADMIN_KEY e a senha do banco
docker compose up -d
```

Acesse `http://SEU_SERVIDOR:8080` para abrir o painel e criar a primeira instância.

**Servidor mínimo (VPS):** 1 vCPU, 1 GB de RAM e 10 GB de disco para algumas instâncias. Some
cerca de 50 MB de RAM por instância conectada.

## Configuração

Toda a configuração vem de variáveis de ambiente (`.env`). A lista completa vai ficar no
`.env.example`; as principais:

| Variável | Descrição | Padrão |
|---|---|---|
| `FZ_ADMIN_KEY` | Chave de administrador do painel e das rotas de admin. **Obrigatória.** | — |
| `DATABASE_URL` | Conexão com o PostgreSQL. **Obrigatória.** | — |
| `HTTP_PORT` | Porta da API e do painel | `8080` |
| `PUBLIC_URL` | Endereço público do servidor (usado nos links de mídia) | — |
| `MEDIA_STORAGE` | `local` ou `s3` | `local` |
| `WEBHOOK_TIMEOUT` | Tempo limite do webhook, em segundos | `10` |
| `DEFAULT_LANGUAGE` | Idioma do painel: `en` ou `pt-BR` | `en` |
| `LOG_LEVEL` | `debug`, `info`, `warn` ou `error` | `info` |

> [!IMPORTANT]
> Nunca suba o arquivo `.env` para o repositório. Use um valor longo e aleatório em
> `FZ_ADMIN_KEY` e sirva o painel com HTTPS (um proxy reverso como Caddy ou Nginx).

## Visão geral da API

Todas as chamadas usam `Authorization: Bearer <chave>` — a chave de administrador nas rotas de
admin, ou a chave da instância nas rotas da instância. A referência completa será publicada em
um arquivo OpenAPI.

| Método | Rota | Descrição |
|---|---|---|
| `POST` | `/instances` | Criar uma instância |
| `GET` | `/instances` | Listar instâncias |
| `POST` | `/instances/{id}/connect` | Iniciar a conexão (gera o QR code quando não está vinculada) |
| `GET` | `/instances/{id}/qr` | Obter o QR code para conectar |
| `POST` | `/instances/{id}/pair` | Conectar com código de pareamento (número de telefone) |
| `POST` | `/instances/{id}/logout` | Desconectar o número |
| `DELETE` | `/instances/{id}` | Apagar a instância |
| `POST` | `/instances/{id}/messages/text` | Enviar mensagem de texto |
| `POST` | `/instances/{id}/messages/media` | Enviar imagem, vídeo, documento ou áudio |
| `POST` | `/instances/{id}/messages/reaction` | Reagir a uma mensagem |
| `GET` | `/instances/{id}/groups` | Listar grupos |
| `POST` | `/instances/{id}/groups` | Criar um grupo |
| `POST` | `/instances/{id}/groups/{jid}/participants` | Adicionar, remover, promover ou rebaixar participantes |
| `PUT` | `/instances/{id}/webhook` | Configurar o webhook |

Exemplo — enviar uma mensagem de texto:

```bash
curl -X POST https://api.exemplo.com/instances/meu-numero/messages/text \
  -H "Authorization: Bearer CHAVE_DA_INSTANCIA" \
  -H "Content-Type: application/json" \
  -d '{"to": "5511999999999", "text": "Olá pela FireZAP API!"}'
```

## Webhooks

Cada instância pode enviar os eventos para um endereço da sua escolha:

| Evento | Quando |
|---|---|
| `message.received` | Chega uma mensagem nova |
| `message.status` | Uma mensagem enviada foi entregue ou lida |
| `connection.update` | A instância conectou, desconectou ou precisa de um QR code novo |
| `group.update` | Mudou o nome, os participantes ou as configurações de um grupo |
| `call.received` | Chegou uma ligação (registrada; a API não atende ligações) |

Toda chamada leva o cabeçalho `X-FireZAP-Signature` (HMAC-SHA256 do corpo com o segredo do seu
webhook), para você confirmar que ela veio do seu servidor. Entregas que falham são reenviadas
com intervalos crescentes.

## Painel

O painel roda junto com a API, na mesma porta, e permite:

- Criar, conectar e remover instâncias (QR code ou código de pareamento)
- Ver o estado de cada instância em tempo real
- Configurar webhooks e chaves de API
- Ler os logs recentes e as entregas de webhook que falharam

A interface está disponível em **inglês** e **português (Brasil)** — escolha no próprio painel
ou defina `DEFAULT_LANGUAGE`.

## Uso responsável

> [!CAUTION]
> **A FireZAP API não tem vínculo, aprovação ou ligação com o WhatsApp ou com a Meta.**
> "WhatsApp" é marca registrada dos seus donos.

O **modo Web** conecta como um dispositivo vinculado, como o WhatsApp Web, o que **não é um
produto oficial do WhatsApp**. O WhatsApp pode restringir ou banir números que descumprem os
[Termos de Serviço](https://www.whatsapp.com/legal/terms-of-service), principalmente por envio
em massa não solicitado. Se você precisa de estabilidade garantida e escala, use o **modo
Cloud API** (oficial).

Este projeto é para comunicação legítima com clientes. Ele **não tem** e **não terá** recursos
para burlar os sistemas anti-abuso do WhatsApp (disfarce de aparelho, atividade falsa, fazendas
de emuladores). O que ele tem, para proteger você e os seus contatos:

- Limite de envio por instância e limites graduais para números recém-conectados
- Opt-out: quem responde com uma palavra de parada não recebe mais mensagens
- Registro claro de tudo o que foi enviado

**Só envie mensagem para quem pediu para receber.** Você é responsável pela forma como usa este
software e por cumprir os termos do WhatsApp e as leis de privacidade, como a LGPD.

## Roadmap

- [ ] **v0.1** — Instâncias (QR e código de pareamento), envio de texto, recebimento, webhooks, sessão persistente
- [ ] **v0.2** — Mídia, áudio, reações, confirmações, fila de envio com limites
- [ ] **v0.3** — Grupos (criar, gerenciar participantes, links de convite)
- [ ] **v0.4** — Painel (inglês e português)
- [ ] **v0.5** — Modo Cloud API (oficial) nos mesmos endpoints
- [ ] **v1.0** — Referência OpenAPI, imagem Docker, ajustes para produção

Ideias e prioridades são discutidas nas [Issues](https://github.com/firezaprepo/firezap-api/issues).

## Como contribuir

Contribuições são bem-vindas! Antes de abrir um pull request:

1. Abra uma issue descrevendo o problema ou o recurso
2. Faça um fork e crie uma branch a partir da `main`
3. Rode `go test ./...` e `go vet ./...` antes de commitar
4. Mantenha código, comentários e mensagens de commit em inglês

Issues e discussões podem ser escritas em inglês ou em português.

## Segurança

Encontrou uma falha de segurança? **Não abra uma issue pública.** Reporte em particular pelo
[GitHub Security Advisories](https://github.com/firezaprepo/firezap-api/security/advisories/new).

## Licença

Distribuída sob a [Licença Apache 2.0](LICENSE).

Construída sobre o [whatsmeow](https://github.com/tulir/whatsmeow) (MPL-2.0) — obrigado aos
seus mantenedores e colaboradores.
