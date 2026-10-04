from pathlib import Path
import re
import subprocess

translations = {
'后端 :8787 / 代理 :8788 / 前端 http://localhost:5173  (Ctrl-C 退出)': '백엔드 :8787 / 프록시 :8788 / 프런트엔드 http://localhost:5173 (종료: Ctrl+C)',
'错误：': '오류: ',
'未知参数：$1（-h 查看用法）': '알 수 없는 옵션: $1(-h로 사용법 확인)',
'部署模式：$MODE': '설치 모드: $MODE',
'输入新密码（用户名固定为 ARTEX）：': '새 비밀번호 입력(사용자 이름: ARTEX): ',
'密码不能为空': '비밀번호는 비워둘 수 없습니다',
'再次输入以确认：': '확인을 위해 다시 입력: ',
'两次输入不一致': '입력한 비밀번호가 일치하지 않습니다',
'从 $cfg 读取数据库配置': '$cfg에서 데이터베이스 설정 읽기',
'本机未找到 psql（请安装 postgresql-client，或改用 -m docker）': 'psql이 없습니다. postgresql-client를 설치하거나 -m docker를 사용하세요',
'缺少数据库用户（-U）或有效的 config.json/DSN': 'DB 사용자(-U) 또는 유효한 config.json/DSN이 필요합니다',
'缺少数据库名（-d）或有效的 config.json/DSN': 'DB 이름(-d) 또는 유효한 config.json/DSN이 필요합니다',
'目标数据库：$target': '대상 데이터베이스: $target',
'确认在该库重置 ARTEX 密码？[y/N] ': '이 데이터베이스의 ARTEX 비밀번호를 재설정할까요? [y/N] ',
'已取消': '취소했습니다',
'写入失败。若报 pgcrypto 权限/缺失，请用具备建扩展权限的角色，或先手动执行 CREATE EXTENSION pgcrypto。': '저장 실패. pgcrypto가 없거나 권한 오류가 나면 확장 생성 권한이 있는 계정을 사용하거나 CREATE EXTENSION pgcrypto를 먼저 실행하세요.',
'未找到 docker': 'docker가 없습니다',
'目标：容器 $CONTAINER 内 psql -U $DUSER -d $DNAME（exec=$EXEC_KIND）': '대상: 컨테이너 $CONTAINER, psql -U $DUSER -d $DNAME(exec=$EXEC_KIND)',
'确认在该容器数据库重置 ARTEX 密码？[y/N] ': '이 컨테이너 DB의 ARTEX 비밀번호를 재설정할까요? [y/N] ',
'写入失败。请确认容器名（-c）、数据库账号（.env 的 POSTGRES_*），以及角色有 pgcrypto 权限。': '저장 실패. 컨테이너 이름(-c), DB 계정(.env의 POSTGRES_*), pgcrypto 권한을 확인하세요.',
'已重置 ARTEX 管理员密码。请用用户名 ARTEX + 新密码登录（无需重启服务）。': 'ARTEX 관리자 비밀번호를 재설정했습니다. ARTEX 계정과 새 비밀번호로 로그인하세요(서비스 재시작 불필요).',
'用法：': '사용법:',
'编译当前系统当前架构': '현재 운영체제와 아키텍처용 빌드',
'编译一个指定目标': '지정 대상 하나 빌드',
'编译并打包全部支持的目标': '지원하는 모든 대상 빌드 및 패키징',
'选项：': '옵션:',
'构建 Linux、macOS、Windows 的 amd64/arm64 目标并生成 zip': '지원하는 Linux·macOS·Windows 아키텍처 빌드 및 zip 생성',
'设置单个目标，例如 windows/amd64': '대상 하나 지정, 예: windows/amd64',
'强制使用 UPX 压缩二进制（可能影响部分 Linux 环境兼容性）': 'UPX로 실행 파일 압축(일부 Linux 환경에서 호환성 문제 가능)',
'不使用 UPX，仅使用 Go linker 裁剪并压缩 zip': 'UPX 없이 Go 링커 최적화 및 zip 압축만 사용',
'显示帮助': '도움말 표시',
'多目标列表可通过 ARTEX_TARGETS 覆盖，例如：': 'ARTEX_TARGETS로 대상 목록 지정 가능, 예:',
'--target 需要 OS/ARCH 参数': '--target에는 OS/ARCH가 필요합니다',
'目标必须是 OS/ARCH，例如 linux/amd64': '대상은 OS/ARCH 형식이어야 합니다. 예: linux/amd64',
'未知参数：$1（使用 --help 查看用法）': '알 수 없는 옵션: $1(--help로 사용법 확인)',
'未检测到 Go（项目需要 Go 1.26 或更高版本）': 'Go가 없습니다. go.mod에 지정된 버전 이상의 Go를 설치하세요',
'ARTEX_SKIP_FRONTEND=1 但 server/webui/dist 不存在': 'ARTEX_SKIP_FRONTEND=1이지만 server/webui/dist가 없습니다',
'未检测到 npm（前端静态构建需要 Node.js/npm）': 'npm이 없습니다. 프런트엔드 빌드에 Node.js/npm이 필요합니다',
'未检测到 rsync': 'rsync가 없습니다',
'构建前端静态资源': '프런트엔드 정적 파일 빌드',
'同步前端资源到 server/webui/dist': '프런트엔드 파일을 server/webui/dist에 복사',
'跳过 UPX：$binary': 'UPX 건너뜀: $binary',
'ARTEX_COMPRESS 必须是 off、auto 或 required': 'ARTEX_COMPRESS는 off, auto 또는 required여야 합니다',
'ARTEX_COMPRESS=required 但未检测到 upx': 'ARTEX_COMPRESS=required이지만 upx가 없습니다',
'未检测到 upx，保留 linker 压缩结果：$binary': 'upx가 없어 링커 최적화 결과 유지: $binary',
'UPX 压缩失败：$binary': 'UPX 압축 실패: $binary',
'UPX 不支持该目标格式，保留未压缩二进制：$binary': 'UPX가 이 대상 형식을 지원하지 않아 원래 실행 파일 유지: $binary',
'UPX 压缩完成：$binary (${before} -> ${after} bytes)': 'UPX 압축 완료: $binary (${before} -> ${after} bytes)',
'打包需要 zip': '패키징에 zip이 필요합니다',
'Release 压缩包：$archive': '배포 압축 파일: $archive',
'无效目标：$target（必须是 OS/ARCH）': '잘못된 대상: $target(OS/ARCH 형식 필요)',
'不支持的系统：$goos（支持 linux、darwin、windows）': '지원하지 않는 운영체제: $goos(linux, darwin, windows 지원)',
'编译 ${goos}/${goarch}，版本 ${ARTEX_BUILD_VERSION}': '${goos}/${goarch} 빌드, 버전 ${ARTEX_BUILD_VERSION}',
'编译完成：$output': '빌드 완료: $output',
'未检测到 sha256sum 或 shasum，跳过 SHA256SUMS': 'sha256sum과 shasum이 없어 SHA256SUMS를 생성하지 않습니다',
'校验文件：$checksum_file': '체크섬 파일: $checksum_file',
'ARTEX_TARGETS 不能为空': 'ARTEX_TARGETS는 비워둘 수 없습니다',
'Release 包已生成于：$ARTEX_PACKAGE_DIR': '배포 패키지 생성 위치: $ARTEX_PACKAGE_DIR',
'找不到可执行文件 $BIN': '실행 파일을 찾을 수 없습니다: $BIN',
'找不到可执行文件 %BIN%': '실행 파일을 찾을 수 없습니다: %BIN%',
'已停止': '중지됨',
'正常退出': '정상 종료',
'请求重启（应用新版本）…': '재시작 요청(새 버전 적용)…',
'异常退出 (code=$code)，${delay}s 后重启': '비정상 종료(code=$code), ${delay}초 후 재시작',
'异常退出 ^(code=!code!^)，!delay!s 后重启': '비정상 종료 ^(code=!code!^), !delay!초 후 재시작',
}
pattern = re.compile('|'.join(re.escape(k) for k in sorted(translations, key=len, reverse=True)))
for name in ['dev.sh', 'reset-password.sh', 'build.sh', 'start.sh', 'start.bat']:
    p = Path(name)
    text = pattern.sub(lambda m: translations[m[0]], p.read_text())
    if name.endswith('.bat') and 'chcp 65001' not in text:
        text = text.replace('@echo off', '@echo off\nchcp 65001 >nul', 1)
    p.write_text(text)
    if name.endswith('.sh'):
        subprocess.run(['bash', '-n', name], check=True)
p = Path('db/schema.sql')
text = p.read_text().replace("'对话 Agent'", "'대화 에이전트'")
text = text.replace('主 agent 调用：取消正在运行的某条 worker 意图，不动其他 worker。', '주 에이전트: 다른 Worker에 영향을 주지 않고 특정 실행 의도를 취소합니다.')
text = text.replace('只读：列出当前共享会话工作区目录内容。', '읽기 전용: 현재 공유 세션 작업 디렉터리의 내용을 나열합니다.')
p.write_text(text)
print('Korean CLI and database seed labels applied')
