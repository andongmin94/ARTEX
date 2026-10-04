from pathlib import Path
import subprocess

p = Path('server/task_templates_test.go')
text = p.read_text()
old = '_, s := newRetestTestServer(t)'
assert old in text
p.write_text(text.replace(old, 's, _ := newRetestServer(t)'))

p = Path('agent/tools.go')
text = p.read_text()
assert 'out["scope"] = t.scopeOverview()' in text
text = text.replace('\n\tout["scope"] = t.scopeOverview()', '')
old = 'out["coverage"] = map[string]any{'
assert old in text
text = text.replace(old, old + '\n\t\t\t\t"scope": t.scopeOverview(),', 1)
p.write_text(text)

p = Path('agent/tools_scope.go')
text = p.read_text()
old = '"value": rule.Value'
assert old in text
# Scope rules store normalized domain/net separately; Raw is the common display value.
p.write_text(text.replace(old, '"value": rule.Raw'))

p = Path('skills/scopesentry/SKILL.md')
text = p.read_text()
text = text.replace('response body, header', 'body, header')
text = text.replace('| subdomain | domain, icp, type, value |', '| subdomain | domain, ip, type, value |')
text = text.replace('| crawler | url, method, body, header |', '| crawler | url, method, body, resultId |')
text = text.replace('| IPAsset | project, port, service, app, tags |', '| IPAsset | project, port, service, app |')
p.write_text(text)
subprocess.run(['gofmt', '-w', 'agent/tools.go', 'agent/tools_scope.go', 'server/task_templates_test.go'], check=True)
print('Scope projection and test fixture corrections applied')
