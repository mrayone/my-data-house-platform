# Cenário NNN — <Título>

- **Perfil:** aplicacional | analítico | operacional
- **Pergunta de negócio:** <uma frase que um gestor faria>
- **Mecanismo:** <MV incremental | Refreshable MV | Dictionary | ASOF JOIN em query> (ADR-0005 — percorra a árvore de decisão antes de escolher)
- **Frescor esperado:** <intervalo, justificado pelo requisito>
- **Janela de recálculo:** <N dias | substituição total | N/A> — justifique contra o p99 de idade de órfão (cenário 009)
- **Entidades envolvidas:** <lista>
- **Tipo de correlação:** <por ID | por coluna não-ID | por código string | chave composta | temporal/intervalo | anti-join | loose | nenhuma>
- **ADRs relevantes:** <lista>

---

## Por que este cenário existe na PoC

Qual capacidade do ClickHouse ele prova e qual risco ele endereça. Se a resposta
for só "porque o negócio pediu", falta a metade técnica.

---

## Fontes

| Tabela | Objeto lido | Camada | Por que este e não outro |
|---|---|---|---|
| | `dh_core.v_<ctx>__<entidade>_current` | L1 | |

Se usar `dictGet`, declare a intenção: o valor gravado é o **vigente no refresh**,
não o vigente no momento do fato. Se o requisito for histórico, o mecanismo é
`ASOF JOIN`, não dictionary.

---

## Modelo de saída

```sql
-- internal/contexts/<ctx>/sql/30-marts/<seq>__<nome>.sql

CREATE TABLE IF NOT EXISTS dh_marts.<nome> {ON_CLUSTER}
(
    ...
    _refreshed_at DateTime64(3)
)
ENGINE = ReplacingMergeTree(_refreshed_at)
PARTITION BY <...>
ORDER BY (<...>);
```

Justifique: escolha do engine, `ORDER BY` (pelo perfil de acesso dominante,
ADR-0011 §1), partição, e cada projection (máximo duas, ADR-0011 §2).

---

## Transformação

```sql
CREATE MATERIALIZED VIEW IF NOT EXISTS dh_marts.mv__<nome> {ON_CLUSTER}
REFRESH EVERY <N> MINUTE APPEND
TO dh_marts.<nome> AS
...
```

SQL completo e executável. Comente onde houver decisão não óbvia. Agregue o lado
1:N **antes** do join, para não multiplicar o cabeçalho.

```sql
CREATE VIEW IF NOT EXISTS dh_marts.v_<nome> {ON_CLUSTER} AS
SELECT * EXCEPT (_refreshed_at) FROM dh_marts.<nome> FINAL;
```

---

## Regras de negócio

Lista numerada e sem ambiguidade. Cubra, no mínimo:

1. Grão da linha.
2. O que conta e o que não conta (pedido cancelado, valor negativo, linha apagada).
3. Tratamento de nulo — e por que `NULL` e não `0` onde for o caso.
4. Moeda (nunca somada entre moedas).
5. Quais colunas **não** são somáveis entre linhas (`countDistinct`, razões).
6. Como razões devem ser recalculadas no consumo.

---

## Armadilhas

O que daria número errado aqui e por quê. Referencie `CLAUDE.md` §8. As mais
frequentes:

- agregador incremental sobre entidade mutável (dupla contagem);
- ler a tabela core sem `FINAL` / sem a view corrente;
- join com o lado 1:N sem agregar antes (multiplicação);
- `IS NULL` em vez de `= ''` para detectar ausência em `LEFT`/`FULL OUTER` JOIN;
- média de razões;
- janela de recálculo menor que o atraso real.

---

## Critérios de aceite

Checklist com query de validação e **valor esperado explícito**. Inclua sempre:

- [ ] paridade com o cálculo independente a partir de `v_*_current`;
- [ ] dedup de CDC (mesma chave em N versões);
- [ ] delete respeitado;
- [ ] late arrival autocorrigido pelo refresh seguinte;
- [ ] refresh saudável em `system.view_refreshes`;
- [ ] latência dentro do alvo do perfil (ADR-0011).

---

## Checks de qualidade associados

| check_id | Regra | Severidade |
|---|---|---|

Marque com **novo** os que ainda não existem em `dh_meta.dq_checks`.

---

## Consumo

Endpoint (se aplicacional) ou query de BI. Mostre a forma **correta** de
recalcular razões.

---

## Arquivos no repositório

| Caminho | Conteúdo |
|---|---|
