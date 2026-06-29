# Demo: how to rotate the demo client secret.
#
# The current demo value lives in TWO places:
#   - keycloak/tbox-realm.json   (client secret stored in the realm export)
#   - (none required for APISIX — apisix.yaml does NOT hardcode the secret,
#      it resolves `${KEYCLOAK_CLIENT_SECRET}` at compose-time via env_file.)
#
# Rotation procedure (demo):
#   1. Generate a fresh secret:
#        openssl rand -hex 32
#   2. Update .env:
#        KEYCLOAK_CLIENT_SECRET=<new-value>
#   3. Update the realm export so next `docker compose down -v && up` still works:
#        keycloak/tbox-realm.json  -> clients[0].secret = "<new-value>"
#   4. Bring the stack down so Keycloak re-imports:
#        docker compose down -v
#        docker compose up -d
#
# Why two places? Keycloak `start-dev --import-realm` reads the realm JSON only at
# first boot. After that the secret lives in the DB and must be rotated via the
# Keycloak admin REST API or by wiping the DB volume and re-importing.
#
# Production note: this is a demo. Do NOT keep the client secret in version control.
# Add a CI check that fails if `keycloak/tbox-realm.json` contains any literal
# `secret` value other than the env placeholder.
