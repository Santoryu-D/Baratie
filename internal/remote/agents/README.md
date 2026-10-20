# Agents pour seedbox

La compilation des versions (workflow Release) dépose ici `d3x-agent-linux-amd64.gz` et `d3x-agent-linux-arm64.gz` : l'agent (`cmd/d3x-agent`, sans interface) compilé pour Linux, compressé, embarqué dans D3X et copié sur la seedbox à la première connexion. Ils ne sont pas suivis par git (`.gitignore`) ; une compilation de développement n'en contient pas.

Pour en produire en local : `sh scripts/build-agents.sh`.
