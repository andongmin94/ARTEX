from pathlib import Path
import json
import subprocess

for name, pairs in json.loads(Path('.localization/test-patches.json').read_text()).items():
    p = Path(name)
    text = p.read_text()
    for old, new in pairs:
        if old in text:
            text = text.replace(old, new)
        elif new not in text:
            raise RuntimeError(f'Patch context changed: {name}: {old}')
    p.write_text(text)

p = Path('sidequestion/context.go')
text = p.read_text()
old = '''		if EstimateInputTokens(req)+req.MaxTokens+512 > b.window {
			return "", ErrContextBudget
		}'''
new = '''		if EstimateInputTokens(req)+req.MaxTokens+512 > b.window {
			// Localization and integer rounding can make the byte estimate differ
			// from the request estimator. Keep the largest UTF-8 prefix accepted
			// by the actual guard; the remaining text stays in the next chunk.
			runes := []rune(part)
			lo, hi := 0, len(runes)
			for lo < hi {
				mid := lo + (hi-lo+1)/2
				req.Messages = []llm.Message{llm.UserText("[이전 요약]\\n" + prior + "\\n[새 자료 일부]\\n" + string(runes[:mid]))}
				if EstimateInputTokens(req)+req.MaxTokens+512 <= b.window {
					lo = mid
				} else {
					hi = mid - 1
				}
			}
			if lo == 0 {
				return "", ErrContextBudget
			}
			part = string(runes[:lo])
			n = len(part)
			req.Messages = []llm.Message{llm.UserText("[이전 요약]\\n" + prior + "\\n[새 자료 일부]\\n" + part)}
		}'''
if old in text:
    p.write_text(text.replace(old, new))
elif new not in text:
    raise RuntimeError('Context budget patch no longer matches')

p = Path('sidequestion/context_test.go')
text = p.read_text()
if 'func TestSideKoreanLongRepliesRespectSmallWindow' not in text:
    start = text.index('func TestSideTwentyLongRepliesRespectSmallWindow')
    end = text.index('\nfunc TestSideOverflowWithoutReduction', start)
    korean = text[start:end].replace('TestSideTwentyLongRepliesRespectSmallWindow', 'TestSideKoreanLongRepliesRespectSmallWindow').replace('strings.Repeat("evidence ", 3000)', 'strings.Repeat("한국어 증거 ", 1500)')
    p.write_text(text[:end] + '\n' + korean + text[end:])

changed = subprocess.check_output(['git', 'diff', '--name-only'], text=True).splitlines()
for name in changed:
    if name.endswith('.go'):
        subprocess.run(['gofmt', '-w', name], check=True)
print('Updated localized assertions and UTF-8 context budget regression coverage')
