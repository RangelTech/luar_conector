# luar_conector

Hub e conector de computadores do LUAR (Spec 11 §26), em Go 1.22. Permite que um computador do cliente se conecte ao LUAR por WebSocket de saída para receber tarefas, sem abrir porta.

- `cmd/hub`: servidor que recebe as conexões (implantado como o serviço `luar-conector-hub` pelo `vps_rt_infra`).
- `cmd/connector`: programa que roda no computador do cliente e mantém a conexão.
- `internal/wsconn`: camada de WebSocket usada pelos dois.
- `scripts/`: `build_kits.sh` (kits por sistema), `install.sh`, `luar-pc` e `luar-relay.py` (relay de linha de comando), `mklatest.py`.
- `tests/e2e.sh`: teste de ponta a ponta.

Versão: tag `v*` publica a imagem e o manifesto; o deploy é só pelo `vps_rt_infra` (`deploy.yml -f service=luar-conector-hub`). Specs: `luar_docs/specs/11`.

Mapa geral do ecossistema: skill `ecossistema_rt` (repositório `personal-skills`).
