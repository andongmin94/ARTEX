from pathlib import Path

p = Path('.localization/finalize.py')
text = p.read_text()
text = text.replace(
    '    if old in text:\n        p.write_text(text.replace(old, new))\n    elif new not in text:',
    '    if new in text:\n        return\n    if old in text:\n        p.write_text(text.replace(old, new))\n    else:',
)
lines = []
for line in text.splitlines():
    if line.startswith("replace('web/src/lib/api.ts', 'import { auth }"):
        lines.extend([
            "p = Path('web/src/lib/api.ts')",
            "text = p.read_text()",
            "if 'import { ApiError }' not in text:",
            "    p.write_text('import { ApiError } from \"./api-error\";\\n' + text)",
        ])
    elif line.startswith("replace('web/src/components/ui/calendar.tsx', 'import "):
        lines.extend([
            "p = Path('web/src/components/ui/calendar.tsx')",
            "text = p.read_text()",
            "if 'import { ko }' not in text:",
            "    text = text.replace('import * as React', 'import { ko } from \"react-day-picker/locale\";\\nimport * as React')",
            "p.write_text(text)",
        ])
    elif line.startswith("replace('web/src/components/ui/calendar.tsx', '    <DayPicker"):
        lines.append("replace('web/src/components/ui/calendar.tsx', '\\n  locale,', '\\n  locale = ko,')")
    else:
        lines.append(line)
p.write_text('\n'.join(lines) + '\n')
