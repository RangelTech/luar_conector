# luar_conector

Hub e conector de computadores do LUAR (Spec 11 §26), em Go 1.22. O computador do cliente abre uma conexão de saída até o hub (sem abrir porta); o hub mantém a presença (online ou offline) e repassa os bytes entre o ssh do ambiente da empresa e o servidor SSH embutido no computador. O hub não conhece segredos de SSH: só encaminha.

- `cmd/hub`: servidor que recebe as conexões (implantado como o serviço `luar-conector-hub` pelo `vps_rt_infra`; endereço `conector.rangeltech.net`).
- `cmd/connector`: programa `luar-conector` do computador do cliente. Comandos: `pair --hub <url> --code <código>` (parear), `install` (instalar como serviço), `run`, `uninstall`, `status`, `version`.
- `internal/wsconn`: camada de WebSocket usada pelos dois.
- `scripts/`: `build_kits.sh` (kits por sistema), `install.sh`, `luar-pc` e `luar-relay.py` (relay de linha de comando), `mklatest.py`.
- `tests/e2e.sh`: teste de ponta a ponta.

Versão: tag `v*` publica a imagem e o manifesto; o deploy é só pelo `vps_rt_infra` (`deploy.yml -f service=luar-conector-hub`). Specs: `luar_docs/specs/11`.

Mapa geral do ecossistema: skill `ecossistema_rt` (repositório `personal-skills`).
