from pathlib import Path
import subprocess


def replace(path, old, new):
    text = path.read_text(encoding='utf-8')
    if new in text:
        return
    if old not in text:
        raise RuntimeError(f'Unexpected source in {path}: {old!r}')
    path.write_text(text.replace(old, new, 1), encoding='utf-8')


replace(Path('server/task_templates_test.go'),
        '_, s := newRetestTestServer(t)', 's, _ := newRetestServer(t)')

# Coverage already uses a local map. Retain its scope contract independently
# of whether numerical coverage is enabled; no scope membership is changed.
p = Path('agent/tools.go')
text = p.read_text(encoding='utf-8')
text = text.replace('\n\tout["scope"] = t.scopeOverview()', '')
p.write_text(text, encoding='utf-8')
replace(p, 'm := map[string]any{}\n\t\t\tif !t.coverageDisabled {',
        'm := map[string]any{"scope": t.scopeOverview()}\n\t\t\tif !t.coverageDisabled {')

# Preserve the user's spelling/casing in the read-only display projection.
# Normalized Value remains in the DB for matching and is not mutated.
replace(Path('agent/tools_scope.go'), '"value": rule.Value', '"value": rule.Raw')
replace(Path('agent/tools_scope.go'),
        'keywords = append(keywords, rule.Value)', 'keywords = append(keywords, rule.Raw)')

p = Path('skills/scopesentry/SKILL.md')
text = p.read_text(encoding='utf-8')
text = text.replace('response body, header', 'body, header')
text = text.replace('| subdomain | domain, icp, type, value |', '| subdomain | domain, ip, type, value |')
text = text.replace('| crawler | url, method, body, header |', '| crawler | url, method, body, resultId |')
text = text.replace('| IPAsset | project, port, service, app, tags |', '| IPAsset | project, port, service, app |')
p.write_text(text, encoding='utf-8')
subprocess.run(['gofmt', '-w', 'agent/tools.go', 'agent/tools_scope.go', 'server/task_templates_test.go'], check=True)
print('Scope projection and test fixture corrections applied')
