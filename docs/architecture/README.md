# Arquitetura

| Documento | O que responde |
|---|---|
| [`overview.md`](overview.md) | o problema, o desenho em uma frase, as quatro decisões que definem o sistema |
| [`repository-structure.md`](repository-structure.md) | o mapa anotado das pastas como construídas e como os artefatos se encadeiam |
| [`layered-model.md`](layered-model.md) | o que cada camada (L0→L3) faz e, principalmente, o que ela não faz |
| [`data-flow.md`](data-flow.md) | o caminho de uma mensagem e os sete casos difíceis do CDC |
| [`fan-in.md`](fan-in.md) | como N tópicos convergem num agregado, e por que a convergência só acontece em L2 |
| [`storage.md`](storage.md) | a física do armazenamento: engines, parts, partições, índice, TTL e volumes |
| [`extensibility.md`](extensibility.md) | como plugar o tópico 31 sem escrever Go |

Decisões e alternativas descartadas: [`../adr/README.md`](../adr/README.md).
