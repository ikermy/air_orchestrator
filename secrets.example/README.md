# File-based secrets for `app_config`

This directory contains examples only.

## Local setup

Create a real `secrets/` directory next to `docker-compose.yml` and copy these files:

```powershell
New-Item -ItemType Directory -Force -Path .\secrets
Copy-Item .\secrets.example\app_master_key.txt.example .\secrets\app_master_key.txt
Copy-Item .\secrets.example\new_app_master_key.txt.example .\secrets\new_app_master_key.txt
```

Then replace the placeholder values with real random secrets.

## Runtime mapping

`docker-compose.yml` mounts:

- `./secrets/app_master_key.txt` -> `/run/secrets/app_master_key.txt`
- `./secrets/new_app_master_key.txt` -> `/run/secrets/new_app_master_key.txt`

## Modes

### Normal mode

In `docker-compose.yml`:

```yaml
- APP_CONFIG_REKEY=false
```

Only `app_master_key.txt` is used.

### Rekey mode

In `docker-compose.yml` temporarily set:

```yaml
- APP_CONFIG_REKEY=true
```

Then:
- old key must be in `app_master_key.txt`
- new key must be in `new_app_master_key.txt`

The app will re-encrypt sensitive `app_config` values and exit without starting the HTTP server.

