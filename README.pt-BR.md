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
- [Mídia](#mídia)
- [Reações](#reações)
- [Responder, editar e apagar](#responder-editar-e-apagar)
- [Grupos](#grupos)
- [Limites de envio](#limites-de-envio)
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
| `PUBLIC_URL` | Endereço público do servidor | — |
| `MEDIA_MAX_MB` | Maior arquivo enviado ou baixado, em MB (1 a 100). O arquivo fica na memória durante o envio | `16` |
| `SEND_MIN_INTERVAL_MS` | Tempo mínimo entre duas mensagens da mesma instância, em milissegundos | `1000` |
| `SEND_MAX_PER_MINUTE` | Máximo de mensagens que uma instância envia em quaisquer 60 segundos (`0` = sem limite) | `30` |
| `SEND_MAX_PER_HOUR` | Máximo de mensagens que uma instância envia em quaisquer 60 minutos (`0` = sem limite) | `600` |
| `SEND_QUEUE_MAX` | Máximo de pedidos de envio esperando a vez em cada instância; os demais recebem `429` | `50` |
| `WEBHOOK_TIMEOUT` | Tempo limite do webhook, em segundos | `10` |
| `WEBHOOK_ALLOW_PRIVATE` | Permite webhook para loopback e redes privadas (mesma máquina ou rede Docker). Deixe `false` se outras pessoas puderem configurar webhooks; os metadados da nuvem continuam bloqueados | `false` |
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
| `POST` | `/instances/{id}/media/download` | Baixar um arquivo recebido |
| `POST` | `/instances/{id}/messages/reaction` | Reagir a uma mensagem |
| `POST` | `/instances/{id}/messages/edit` | Editar o texto de uma mensagem enviada |
| `POST` | `/instances/{id}/messages/delete` | Apagar uma mensagem para todos |
| `GET` | `/instances/{id}/groups` | Listar grupos |
| `POST` | `/instances/{id}/groups` | Criar um grupo |
| `GET` | `/instances/{id}/groups/{jid}` | Dados do grupo com os membros |
| `PATCH` | `/instances/{id}/groups/{jid}` | Mudar nome, descrição, `announce` ou `locked` |
| `POST` | `/instances/{id}/groups/{jid}/participants` | Adicionar, remover, promover ou rebaixar participantes |
| `GET` | `/instances/{id}/groups/{jid}/invite` | Obter o link de convite |
| `POST` | `/instances/{id}/groups/{jid}/invite/reset` | Revogar o link de convite e gerar outro |
| `POST` | `/instances/{id}/groups/join` | Entrar num grupo pelo link de convite |
| `POST` | `/instances/{id}/groups/{jid}/leave` | Sair de um grupo |
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
| `message.status` | Uma mensagem enviada foi entregue (`delivered`), lida (`read`) ou ouvida/assistida (`played`, em mensagem de voz e vídeo) |
| `connection.update` | A instância conectou, desconectou ou precisa de um QR code novo |
| `group.update` | Mudou o nome, a descrição, as configurações ou os membros de um grupo, ou este número entrou num grupo |
| `call.received` | Chegou uma ligação (registrada; a API não atende ligações) |

`message.received` traz `sender`/`chat` como o WhatsApp manda — muitas vezes um LID (`…@lid`), que não é número de telefone — e também `senderPn` e, em conversa privada, `chatPn` com o JID do telefone quando ele é conhecido. `chatType` diz o tipo de conversa: `private`, `group`, `status` (Status do WhatsApp), `broadcast` ou `newsletter` (Canal) — filtre os que você não trata. Mensagens apagadas e editadas chegam com `type` `revoke` ou `edit` e o id da mensagem afetada em `target`; reações chegam com `type` `reaction`, o emoji em `text` (vazio quando a reação foi retirada) e a mensagem reagida em `target`. Uma mensagem que responde a outra traz `replyTo` com o `id` e o `participant` da mensagem respondida.

`message.status` traz os `ids` das mensagens, o `chat` e o `status`. Em grupo cada participante manda a sua própria confirmação, identificada por `participant` e `participantPn`; em conversa privada, `chatPn` traz o telefone quando ele é conhecido.

O endereço do webhook precisa ser `http` ou `https` e não pode apontar para loopback, redes privadas ou link-local (ver `WEBHOOK_ALLOW_PRIVATE`). O segredo que você envia nunca é devolvido; se você deixar em branco, um é gerado e mostrado uma única vez.

Toda chamada leva o cabeçalho `X-FireZAP-Signature` (HMAC-SHA256 do corpo com o segredo do seu
webhook), para você confirmar que ela veio do seu servidor. Entregas que falham são reenviadas
com intervalos crescentes.

## Mídia

O `message.received` de imagem, vídeo, áudio, documento e figurinha traz um objeto `media`
(mimetype, tamanho, fileName, segundos, `ptt` para mensagem de voz e as chaves para baixar o
arquivo). A API não guarda cópia de mensagens nem de arquivos: para obter o arquivo, devolva esse
objeto sem alterar:

```bash
curl -X POST https://api.exemplo.com.br/instances/meu-numero/media/download   -H "Authorization: Bearer CHAVE_DA_INSTANCIA" -H "Content-Type: application/json"   -d '<o objeto "media" do webhook>' -o arquivo
```

A resposta é o arquivo já decifrado, com o `Content-Type` dele. O WhatsApp guarda a mídia por
tempo limitado: arquivo antigo responde `410 Gone`, então baixe o que precisar quando chegar.

Para enviar, use `POST /instances/{id}/messages/media` com `to`, `type` (`image`, `video`,
`audio` ou `document`) e o arquivo em `url` **ou** `base64` (aceita `data:` URL), além de
`caption`, `fileName`, `mimetype` e `ptt`, opcionais:

```bash
curl -X POST https://api.exemplo.com.br/instances/meu-numero/messages/media   -H "Authorization: Bearer CHAVE_DA_INSTANCIA" -H "Content-Type: application/json"   -d '{"to": "5511999999999", "type": "document", "url": "https://exemplo.com.br/boleto.pdf", "caption": "Seu boleto"}'
```

Imagem precisa ser JPEG ou PNG e vídeo MP4; mensagem de voz (`"ptt": true`) precisa ser
OGG/Opus — outros formatos são recusados em vez de chegarem quebrados no celular; mande-os como
documento. URLs de mídia seguem as mesmas regras de endereço do webhook. O tamanho é limitado por
`MEDIA_MAX_MB`.

## Reações

Reaja com `POST /instances/{id}/messages/reaction`, usando os valores do evento
`message.received`: `chat`, o `id` da mensagem em `messageId` e `fromMe` quando a mensagem foi
enviada pelo seu número. Em grupo, reagir à mensagem de outra pessoa também exige o `sender`
dela em `participant`. `emoji` vazio retira a sua reação.

```bash
curl -X POST https://api.exemplo.com.br/instances/meu-numero/messages/reaction \
  -H "Authorization: Bearer CHAVE_DA_INSTANCIA" -H "Content-Type: application/json" \
  -d '{"chat": "5511999999999@s.whatsapp.net", "messageId": "3EB0F1E313827D8CDEB5AE", "emoji": "👍"}'
```

## Responder, editar e apagar

**Responder** — acrescente `replyTo` a uma mensagem de texto ou mídia: o `messageId` que está
sendo respondido, `fromMe` quando ele foi enviado pelo seu número, `participant` para a mensagem
de outra pessoa em grupo, e `text` para aparecer dentro do balão da citação. A API não guarda
mensagens, então não consegue preencher o texto citado sozinha — sem `text` o celular mostra a
resposta com a citação vazia.

```bash
curl -X POST https://api.exemplo.com.br/instances/meu-numero/messages/text \
  -H "Authorization: Bearer CHAVE_DA_INSTANCIA" -H "Content-Type: application/json" \
  -d '{"to": "5511999999999", "text": "Temos sim!", "replyTo": {"messageId": "3EB0F1E313827D8CDEB5AE", "text": "Tem na cor azul?"}}'
```

**Editar** — `POST /instances/{id}/messages/edit` com `chat`, `messageId` e o novo `text`. Só
mensagens de texto enviadas pelo seu número; o WhatsApp aceita edição por cerca de 15 minutos
depois do envio e ignora as posteriores.

**Apagar para todos** — `POST /instances/{id}/messages/delete` com `chat`, `messageId` e
`fromMe: true` para as suas mensagens. Em grupo em que o seu número é administrador, também dá
para apagar a mensagem de outra pessoa informando `participant`. O WhatsApp limita a idade da
mensagem que pode ser apagada.

Respostas, edições e exclusões passam pela mesma fila e pelos mesmos limites das outras mensagens.

## Grupos

`{jid}` é o id do grupo (`120363…@g.us`, ou só os dígitos). Alterações exigem que este número
seja administrador do grupo, como no celular.

- **Criar** — `POST /instances/{id}/groups` com `name` (até 100 caracteres) e, opcionalmente,
  `participants` (telefones ou ids). Sem participantes o grupo começa só com o seu número:
  compartilhe o link de convite.
- **Membros** — `POST /instances/{id}/groups/{jid}/participants` com `action` (`add`, `remove`,
  `promote` ou `demote`) e até 50 `participants`. O WhatsApp responde pessoa por pessoa:
  confira `error` em cada item — `403` costuma ser a privacidade da pessoa, que não permite
  ser adicionada; mande o link de convite.
- **Configurações** — `PATCH /instances/{id}/groups/{jid}` com `name`, `description`,
  `announce` (só administradores enviam mensagens) e/ou `locked` (só administradores editam
  os dados do grupo).
- **Links de convite** — `GET …/invite` devolve o link; `POST …/invite/reset` revoga e
  devolve um novo. `POST /instances/{id}/groups/join` com `link` entra num grupo.

`group.update` informa só os campos que mudaram: `name`, `description`, `announce`, `locked`,
`join`, `leave`, `promote`, `demote`, `inviteReset` (o link em si nunca é enviado) e
`joined: true` quando este número entra ou é adicionado a um grupo.

> [!WARNING]
> Adicionar pessoas que não pediram para estar num grupo é o tipo de spam mais denunciado no
> WhatsApp. **Cada pessoa adicionada conta como uma mensagem** nos limites de envio (criar um
> grupo com 20 pessoas conta 21), e um pedido maior que `SEND_MAX_PER_MINUTE` é recusado.
> Prefira links de convite.

## Limites de envio

Toda mensagem que uma instância envia — texto, mídia, reação ou alteração de grupo — passa por uma fila da própria
instância: uma por vez, na ordem de chegada, respeitando `SEND_MIN_INTERVAL_MS`,
`SEND_MAX_PER_MINUTE` e `SEND_MAX_PER_HOUR`. Cada instância tem os seus contadores, então um
número movimentado nunca atrasa os outros.

O pedido espera a vez enquanto isso cabe no tempo limite dele. Quando a fila está cheia
(`SEND_QUEUE_MAX`) ou os limites segurariam a mensagem por tempo demais, a API responde
`429 Too Many Requests` com o cabeçalho `Retry-After` e `retryAfter` (segundos) no corpo —
**nada foi enviado**, então é seguro tentar de novo depois. `GET /instances/{id}` mostra a
atividade atual em `sending`: `queued`, `lastMinute` e `lastHour`.

Os padrões servem para atendimento. São uma proteção, não uma garantia: o WhatsApp decide
sozinho o que considera abuso, e nenhum limite torna seguro o envio em massa não solicitado.

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

- Limites de envio por instância: intervalo mínimo, por minuto e por hora (ver [Limites de envio](#limites-de-envio))

Planejado:

- Limites graduais para números recém-conectados
- Opt-out: quem responde com uma palavra de parada não recebe mais mensagens
- Registro claro de tudo o que foi enviado

**Só envie mensagem para quem pediu para receber.** Você é responsável pela forma como usa este
software e por cumprir os termos do WhatsApp e as leis de privacidade, como a LGPD.

## Roadmap

- [x] **v0.1** — Instâncias (QR e código de pareamento), envio de texto, recebimento, webhooks, sessão persistente
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
