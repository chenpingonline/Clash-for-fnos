# fnOS packaging repository

- Shared Vue / Go / translations and Docker are maintained in ../Clash-Manager, not copied into this repository.
- Pin shared source with upstream.lock (full commit SHA + VERSION); never silently build a branch head or dirty source.
- Preserve fnOS appname, paths, lifecycle, data compatibility and icon configuration.
- fpk/manifest is the FPK version source. Compile it into the isolated shared build and package its matching CHANGELOG.
- Run ./scripts/check.sh; build x86 / arm / all serially and run scripts/audit-fpk.py for packaging changes.
- Keep final artifacts in dist/. Archive proof does not prove real-device installation or TUN behavior.
- Commit, Git push, FPK publication and Docker publication are separate authorization boundaries.
