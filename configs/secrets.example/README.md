# File-based secrets for `app_config`

This directory contains examples only.

## Local setup

Create a real `configs/secrets/` directory and copy these files:

```powershell
New-Item -ItemType Directory -Force -Path .\configs\secrets
Copy-Item .\configs\secrets.example\app_master_key.txt.example .\configs\secrets\app_master_key.txt
Copy-Item .\configs\secrets.example\new_app_master_key.txt.example .\configs\secrets\new_app_master_key.txt
```

Then replace the placeholder values with real random secrets.

## Runtime mapping

`build/dev.yml` and `build/prod.yml` mount:

- `../configs/secrets/app_master_key.txt` -> `/run/secrets/app_master_key.txt`
- `../configs/secrets/new_app_master_key.txt` -> `/run/secrets/new_app_master_key.txt`

## Modes

### Normal mode

In `build/dev.yml` / `build/prod.yml`:

```yaml
- APP_CONFIG_REKEY=false
```

Only `app_master_key.txt` is used.

### Rekey mode

In `build/dev.yml` / `build/prod.yml` temporarily set:

```yaml
- APP_CONFIG_REKEY=true
```

Then:
- old key must be in `app_master_key.txt`
- new key must be in `new_app_master_key.txt`

The app will re-encrypt sensitive `app_config` values and exit without starting the HTTP server.

