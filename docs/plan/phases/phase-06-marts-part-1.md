# Fase 06 — Marts, parte 1: cenários 001 a 005

- **Branch:** `feat/phase-06-marts-part-1`
- **Pré-requisito:** Fase 05 `DONE`
- **Bloqueia:** Fase 07
- **ADRs relevantes:** [0005](../../adr/0005-estrategia-de-agregacao-mv-refreshable-dictionary.md), [0004](../../adr/0004-semantica-cdc-dedup-ordem-delete.md), [0011](../../adr/0011-serving-aplicacional-e-analitico-no-mesmo-store.md)
- **Entrega verificável:** 5 marts com paridade contra cálculo independente e autocorreção de late arrival provada

## Objetivo da fase

Construir a camada de agregação. Aqui a PoC responde à pergunta central: **o
ClickHouse agrega N tópicos correlacionados usando só mecanismos nativos?**

Cada tarefa implementa **um cenário já especificado** em
[`../../scenarios/`](../../scenarios/). O doc do cenário é a especificação: DDL,
SQL, regras de negócio, armadilhas e critérios de aceite já estão lá. **Não
reinterprete** — se o doc estiver errado, corrija o doc primeiro, no mesmo commit.

> **Paralelizável.** As tarefas de cenário são independentes: arquivos distintos,
> tabelas distintas. Podem ser executadas por agentes diferentes em branches a
> partir de `feat/phase-06-marts-part-1`. A única ordem obrigatória é T01
> (dictionaries) antes de T03 e T05.

### Antes de qualquer tarefa desta fase

Leia a **árvore de decisão do ADR-0005** e confirme que o mecanismo do doc do
cenário é o que a árvore indica. Se divergir, é bug no doc — pare e corrija.

---

## P06-T01 — Dictionaries de cadastro

**Objetivo:** `business_unit` e `discount_codes` como dictionaries.

**Arquivos:**
- `internal/contexts/organization/sql/30-marts/0010__dict_business_unit.sql`
- `internal/contexts/pricing/sql/30-marts/0020__dict_discount_codes.sql`

**Especificação:** conforme ADR-0005 §3 e `core.expose_as_dictionary` dos
contratos.
```sql
CREATE DICTIONARY IF NOT EXISTS dh_core.dict__business_unit {ON_CLUSTER}
( bu_code String, bu_name String, channel String,
  region String, country String, cost_center String, active UInt8 )
PRIMARY KEY bu_code
SOURCE(CLICKHOUSE(QUERY 'SELECT bu_code, bu_name, channel, region, country, cost_center, active FROM dh_core.v_organization__business_unit_current'))
LAYOUT(COMPLEX_KEY_HASHED())
LIFETIME(MIN 300 MAX 600);
```
- A `SOURCE` lê a **view corrente**, nunca a tabela.
- `LAYOUT` e `LIFETIME` vêm do contrato.
- O de `discount_codes` inclui os atributos declarados no contrato
  (`campaign_name`, `discount_type`, `discount_value`, `valid_from`, `valid_to`,
  `max_uses`, `business_unit_scope`).
- **Atenção:** com `COMPLEX_KEY_HASHED`, `dictGet` exige `tuple(chave)` mesmo com
  chave de uma coluna. Documente isso em comentário no SQL — é erro comum.

**Critérios de aceite:**
- [ ] `SELECT status, element_count, last_exception FROM system.dictionaries` mostra
      os dois `LOADED` sem exceção
- [ ] `dictGet('dh_core.dict__business_unit','bu_name',tuple('BU-TEST'))` retorna valor
- [ ] `dictHas` retorna 0 para chave inexistente (base do achado do cenário 004)
- [ ] `bytes_allocated` registrado em `docs/evaluation/results.md`
- [ ] após um UPDATE de CDC em `business_unit`, o dictionary reflete a mudança em
      até `LIFETIME MAX`

**Validação:** `make migrate && clickhouse-client -q "SELECT name,status,element_count FROM system.dictionaries"`

**Commit:** `feat(organization): adicionar dictionaries de cadastro`

**Docs a atualizar:** `PROGRESS.md`; `docs/evaluation/results.md`

---

## P06-T02 — Cenário 001: `order_360`

**Objetivo:** implementar [`../../scenarios/001-order-360.md`](../../scenarios/001-order-360.md).

**Arquivos:** os listados na seção "Arquivos no repositório" daquele doc.

**Especificação:** o doc do cenário, integralmente. Pontos de atenção:
- Refreshable MV, janela de 3 dias, `REFRESH EVERY 1 MINUTE APPEND`.
- Agregue `order_item` e `order_payment` por `order_id` **antes** do join — senão o
  cabeçalho é multiplicado.
- `ORDER BY (order_id)`: perfil aplicacional.
- Toda leitura de L1 por `v_*_current`.
- O teste de **late arrival** é o item mais importante: item antes do pedido ⇒
  incompleto no 1º refresh, completo no 2º, **sem intervenção**.

**Critérios de aceite:** os do doc do cenário, mais:
- [ ] `system.view_refreshes` com `last_refresh_result = 'Finished'`
- [ ] `no-join-in-incremental-mv.sh` passa (a MV tem `REFRESH`, então é permitida)
- [ ] duração do refresh registrada em `docs/evaluation/results.md`

**Validação:** `make migrate && make seed-adverse && go test ./test/e2e/ -run TestOrder360 -v`

**Commit:** `feat(analytics): implementar cenário 001 - visão 360 do pedido`

**Docs a atualizar:** `PROGRESS.md`; `docs/evaluation/results.md`; o doc do cenário se algo divergiu

---

## P06-T03 — Cenário 002: `revenue_by_bu_day`

**Objetivo:** implementar [`../../scenarios/002-revenue-by-bu-day.md`](../../scenarios/002-revenue-by-bu-day.md).

**Pré-requisito:** P06-T01 (dictionary de BU).

**Especificação:** o doc do cenário. Pontos de atenção:
- `ReplacingMergeTree(_refreshed_at)` no destino, **nunca** `SummingMergeTree` — o
  motivo está na armadilha 1 do doc.
- `Decimal(38,4)` nos agregados (overflow de `Decimal(18,4)` em `sum()`).
- `dictGetOrDefault` com default **inequívoco** (`'(sem cadastro)'`).
- `_bu_found` materializado: BU sem cadastro não é descartada.

**Critérios de aceite:** os do doc, com destaque para:
- [ ] **dupla contagem não ocorre:** v1 (100) + v2 (150) da mesma chave ⇒ 150
- [ ] cancelamento retira a receita e incrementa `orders_canceled`
- [ ] paridade com o cálculo independente = 0
- [ ] cada moeda fecha isoladamente

**Validação:** `go test ./test/e2e/ -run TestRevenueByBU -v`

**Commit:** `feat(analytics): implementar cenário 002 - receita por BU e dia`

**Docs a atualizar:** `PROGRESS.md`; `docs/evaluation/results.md`

---

## P06-T04 — Cenário 003: `payment_funnel_day`

**Objetivo:** implementar [`../../scenarios/003-payment-funnel.md`](../../scenarios/003-payment-funnel.md).

**Especificação:** o doc do cenário. Pontos de atenção:
- `countDistinct(order_id)`, nunca `count()`, para contar pedidos — o grão é a
  tentativa.
- `quantileIf(...)(..., authorized_at IS NOT NULL)`: nulo **não** entra como zero.
- Taxas com denominador explícito e `if(denominador = 0, 0, ...)` — nunca `NaN`.
- `INNER JOIN` com `order`: tentativa órfã vai para o cenário 009, não para o funil.

**Critérios de aceite:** os do doc, com destaque para:
- [ ] tentativa com 3 transições de status conta como **1** tentativa
- [ ] `auth_latency_p50_s` não contaminado por nulo (teste com 9 autorizadas + 1 não)
- [ ] toda taxa em `[0,1]` e nenhuma `NaN`
- [ ] fan-out preservado: 3 tentativas, 1 pedido

**Validação:** `go test ./test/e2e/ -run TestPaymentFunnel -v`

**Commit:** `feat(analytics): implementar cenário 003 - funil de pagamento`

**Docs a atualizar:** `PROGRESS.md`; `docs/evaluation/results.md`

---

## P06-T05 — Cenário 004: `discount_effectiveness`

**Objetivo:** implementar [`../../scenarios/004-discount-effectiveness.md`](../../scenarios/004-discount-effectiveness.md).

**Pré-requisito:** P06-T01 (dictionary de cupons).

**Especificação:** o doc do cenário. Pontos de atenção:
- Normalização `upperUTF8(trimBoth(code))` **uma vez**, na CTE, antes de qualquer
  agrupamento.
- `dictHas` em vez de `INNER JOIN`: cupom sem cadastro é o **achado**, não algo a
  descartar.
- Duas tabelas (efetividade e baseline) — grãos diferentes.
- `dh_reports.v_unknown_discount_codes` filtra `last_seen` recente para não alarmar
  com defasagem do dictionary.

**Critérios de aceite:** os do doc, com destaque para:
- [ ] `PROMO10`, `promo10` e `" PROMO10 "` viram **uma** linha com `orders = 3`
- [ ] `CUPOM-FANTASMA` aparece com `code_found = 0` e em `v_unknown_discount_codes`
- [ ] troca de cupom (v1 `PROMO10` → v2 `PROMO20`) **não** conta o pedido duas vezes
- [ ] estouro de `max_uses` detectado

**Validação:** `go test ./test/e2e/ -run TestDiscountEffectiveness -v`

**Commit:** `feat(analytics): implementar cenário 004 - efetividade de cupom`

**Docs a atualizar:** `PROGRESS.md`; `docs/evaluation/results.md`

---

## P06-T06 — Cenário 005: `item_margin_daily` (ASOF JOIN)

**Objetivo:** implementar [`../../scenarios/005-real-margin-vs-price-list.md`](../../scenarios/005-real-margin-vs-price-list.md).

**Especificação:** o doc do cenário. **Esta é a tarefa tecnicamente mais delicada
da fase.** Pontos de atenção:
- `ASOF LEFT JOIN` com a **desigualdade na última posição** do `ON`; as anteriores
  são igualdades. Fora dessa forma, o ClickHouse não aplica `ASOF` — e um `JOIN`
  comum multiplicaria a linha por todas as vigências, silenciosamente.
- `LEFT` e não `INNER`: item sem preço vigente permanece, com `units_without_price`.
- `sumIf(..., price_valid_from IS NOT NULL)` em **todos** os agregados de "at list"
  e de custo — sem isso, `margin_pct` vai a 100%.
- `toDecimal128(quantity, 4) * list_price`: conversão explícita.
- Janela de **7 dias** (não 3): preço retroativo é comum.
- Moeda na condição do `ASOF`.

**Critérios de aceite:** os do doc, com destaque para:
- [ ] `SKU-ASOF` com 3 vigências: a venda de D-3 usa o preço de D-5 (90,00), não o
      de D-1 nem o de D-10
- [ ] preço com `valid_from` futuro **não** é usado
- [ ] `SKU-SEM-PRECO`: `margin_pct = 0`, nunca `1.0`
- [ ] `SKU-FX`: preço em outra moeda **não** é usado
- [ ] `SKU-DUP`: 3 versões CDC da mesma vigência ⇒ `units_sold` não triplica
- [ ] paridade com o cálculo independente (com `ASOF`) = 0
- [ ] **duração do refresh da janela de 7 dias registrada** em `results.md` — é um
      dos números que pesa na comparação com alternativas

**Validação:** `go test ./test/e2e/ -run TestItemMargin -v`

**Commit:** `feat(analytics): implementar cenário 005 - margem real vs preço de tabela`

**Docs a atualizar:** `PROGRESS.md`; `docs/evaluation/results.md`

---

## P06-T07 — `mart-refresh`, `reports` e e2e de late arrival

**Objetivo:** ferramentas de operação dos marts e a prova de autocorreção.

**Arquivos:**
- `cmd/dhctl/mart.go`
- `internal/reporting/query/runner.go`
- `cmd/dhctl/reports.go`
- `Makefile` (`mart-refresh`, `reports`)
- `test/e2e/late_arrival_test.go`
- `docs/runbooks/mart-operations.md`

**Especificação:**
- `dhctl mart refresh --mart=<nome> [--window=<dias>]`: executa
  `SYSTEM REFRESH VIEW dh_marts.mv__<nome>`; com `--window`, executa um
  `INSERT ... SELECT` dirigido para a janela pedida (para correção retroativa fora
  da janela padrão — o caso previsto na armadilha 7 do cenário 005).
- `dhctl mart status`: `system.view_refreshes` formatado — view, status, último
  sucesso, duração, exceção. Exit 1 se houver exceção.
- `dhctl reports run [--scenario=NNN]`: executa as queries de consumo dos cenários e
  imprime o resultado. `make reports` roda todas.
- **e2e de late arrival, o teste que justifica a escolha de Refreshable MV:**
  1. produzir `order_item` de `ORD-LATE-1` **sem** o pedido;
  2. `dhctl mart refresh --mart=order_360`;
  3. confirmar que `order_360` está **incompleto** (sem a linha, ou com
     `items_count = 0` conforme o doc do cenário);
  4. produzir o pedido `ORD-LATE-1`;
  5. `dhctl mart refresh --mart=order_360`;
  6. confirmar que agora está **completo**, sem nenhuma intervenção manual.

  Repetir o mesmo padrão para `revenue_by_bu_day`.
- `docs/runbooks/mart-operations.md`: como forçar refresh, como diagnosticar refresh
  travado, como corrigir dado fora da janela, como mudar o intervalo de refresh, e
  o que olhar em `system.view_refreshes`.

**Critérios de aceite:**
- [ ] `make mart-refresh MART=order_360` funciona
- [ ] `dhctl mart status` sai 1 se alguma Refreshable MV tiver exceção
- [ ] `make reports` imprime resultado dos 5 cenários
- [ ] **o e2e de late arrival passa nos 6 passos**, para `order_360` e para
      `revenue_by_bu_day`
- [ ] `--window` corrige dado fora da janela padrão

**Validação:** `go test ./test/e2e/ -run TestLateArrival -v && make reports`

**Commit:** `feat(analytics): adicionar operação de marts e e2e de late arrival`

**Docs a atualizar:** `PROGRESS.md`; `docs/runbooks/mart-operations.md`

---

## Critérios de aceite da fase

- [ ] 2 dictionaries `LOADED`
- [ ] 5 marts implementados conforme os docs dos cenários 001-005
- [ ] **paridade = 0** em todos os cinco, contra cálculo independente a partir de
      `v_*_current`
- [ ] **nenhuma dupla contagem** após UPDATE de CDC (cenários 002 e 004)
- [ ] **autocorreção de late arrival provada** por e2e
- [ ] `ASOF JOIN` escolhendo a vigência correta (cenário 005)
- [ ] toda Refreshable MV com `last_refresh_result = 'Finished'`
- [ ] `make verify` passa, incluindo os checks estáticos da Fase 05
- [ ] durações de refresh e `bytes_allocated` dos dictionaries em `results.md`
- [ ] `PROGRESS.md` com a Fase 06 `DONE`
