import hashlib
from pathlib import Path


def read_exact(path, sha):
    raw = Path(path).read_bytes()
    actual = hashlib.sha1(f'blob {len(raw)}\0'.encode() + raw).hexdigest()
    assert actual == sha, f'{path} changed: {actual}; refusing to overwrite'
    return raw.decode('utf-8')


def replace_one(text, before, after):
    assert text.count(before) == 1, f'Expected one occurrence: {before[:100]!r}'
    return text.replace(before, after, 1)


path = 'traffic/traffic.go'
text = read_exact(path, 'a05d8c2cd7e4806bb41274ebb7ffebfb9847ab2c')
text = replace_one(text, '\t"github.com/Autumn-27/artex/db"', '\t"github.com/Autumn-27/artex/db"\n\t"github.com/Autumn-27/artex/internal/sqlitedb"')
text = replace_one(text, '\n\t_ "modernc.org/sqlite"', '')
text = replace_one(text, 'func Open(dir, addr string) (*Traffic, error) {', '''func Open(dir, addr string) (*Traffic, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	dir = root''')
start = text.index('\t// busy_timeout is a per-connection setting')
end = text.index('\tt := &Traffic{', start)
text = text[:start] + '''	index := filepath.Join(dir, "_index", "index.sqlite")
	db, err := sqlitedb.Open(context.Background(), index)
	if err != nil {
		return nil, err
	}
''' + text[end:]
start = text.index('\t// Re-applied rather than left to the DSN')
end = text.index('\tvar tables int', start)
text = text[:start] + text[end:]
Path(path).write_text(text, encoding='utf-8')

path = 'server/auth.go'
text = read_exact(path, '567a278697c91ef2644ab01a9f4e0456176d75e3')
text = replace_one(text, '''	hash, _, _ := pg.GetSetting(authPassKey)
	writeJSON(w, 200, map[string]any{"initialized": hash != ""})''', '''	hash, exists, err := pg.GetSetting(authPassKey)
	if err != nil || (exists && hash == "") {
		writeErr(w, 500, "인증 설정을 읽을 수 없습니다")
		return
	}
	writeJSON(w, 200, map[string]any{"initialized": exists})''')
text = replace_one(text, '''	existing, _, _ := pg.GetSetting(authPassKey)
	if existing != "" {''', '''	existing, exists, err := pg.GetSetting(authPassKey)
	if err != nil || (exists && existing == "") {
		writeErr(w, 500, "인증 설정을 읽을 수 없습니다")
		return
	}
	if exists {''')
text = replace_one(text, '''	if err := pg.SetSetting(authPassKey, string(hash)); err != nil {
		writeErr(w, 500, "저장 실패: "+err.Error())
		return
	}''', '''	created, err := pg.SetSettingIfAbsent(r.Context(), authPassKey, string(hash))
	if err != nil {
		writeErr(w, 500, "인증 설정을 저장할 수 없습니다")
		return
	}
	if !created {
		writeErr(w, 403, "비밀번호가 이미 설정되었습니다")
		return
	}''')
before = '\thash, ok, _ := pg.GetSetting(authPassKey)'
assert text.count(before) == 2
text = text.replace(before, '''	hash, ok, err := pg.GetSetting(authPassKey)
	if err != nil || (ok && hash == "") {
		writeErr(w, 500, "인증 설정을 읽을 수 없습니다")
		return
	}''')
text = replace_one(text, '''	if err := pg.SetSetting(authPassKey, string(newHash)); err != nil {
		writeErr(w, 500, "저장 실패: "+err.Error())
		return
	}''', '''	changed, err := pg.CompareAndSwapSetting(r.Context(), authPassKey, hash, string(newHash))
	if err != nil {
		writeErr(w, 500, "인증 설정을 저장할 수 없습니다")
		return
	}
	if !changed {
		writeErr(w, 409, "다른 요청에서 비밀번호가 변경되었습니다. 다시 로그인하세요")
		return
	}''')
Path(path).write_text(text, encoding='utf-8')

path = Path('docs/development-plan.md')
text = path.read_text(encoding='utf-8')
text = replace_one(text, '다음 구현은 **M2.2 SQLite 저장 기반과 M2.3 실제 부팅/설정 경로 연결**이다.', 'M2.2/M2.3을 작은 검증 단위로 진행한다. 이번 단위는 **공통 SQLite 연결을 기존 트래픽에 적용하고 설정/인증의 실제 저장 경로를 SQLite fixture로 검증**하는 것이다. 전체 업무 스키마와 NewManager/New 부팅 전환은 아직 남아 있다.')
text = replace_one(text, '- [ ] **M2.3 설정·인증:**', '- [ ] **M2.2a 공통 연결/트래픽:** `internal/sqlitedb.Open`을 실제 `traffic.Open`에 연결. URI 특수문자, 연결별 PRAGMA, WAL, 취소, 파일 보존, 재실행/FTS 검사. 코드 반영 후 원격 CI 확인 필요. M2.2 전체 완료가 아니다.\n- [ ] **M2.3a 설정/인증 저장:** 실제 설정 메서드의 SQLite 저장/재실행, 동시 최초 설정/비밀번호 변경의 단일 승자, 조회 오류 fail-closed를 HTTP fixture로 검사. 전체 NewManager/New 부팅이나 LLM 프로필 이식 완료가 아니다.\n- [ ] **M2.3 설정·인증:**')
text += '''

### M2.2a/M2.3a — SQLite 연결 및 인증 저장 구현

- 공통 파일 연결 `internal/sqlitedb.Open`을 기존 트래픽의 실제 생성 경로에서 사용한다. URI 인코딩 없는 DSN과 `?` bare-path 분기를 제거한다.
- 연결마다 foreign_keys=1, busy_timeout=5000, synchronous=FULL을 적용하고, 파일의 WAL 적용 결과를 확인한다. 손상/취소/경로 오류는 데이터를 지우거나 다른 DB로 넘어가지 않고 실패한다.
- 연결 함수는 스키마·쓰기 큐·SQL 호환 계층이 아니다. 트래픽의 기존 쓰기 조정과 트랜잭션은 유지하며, 업무 DB의 쓰기 연결/전체 스키마 초기화는 M2.2에 남아 있다.
- 설정의 CURRENT_TIMESTAMP/원자적 최초 삽입/조건부 변경을 실제 인증 핸들러에 연결한다. 기존 PG 업무 DB를 계속 사용하는 main에서도 이 인증 수정은 실제 사용된다. DB 종류 선택, 이중 기록, PG→SQLite 자동 데이터 변환은 추가하지 않는다.
- 설정/인증 테스트는 최소 settings 테이블을 가진 폐기 가능한 SQLite 파일에서 실행한다. 실제 HTTP 요청 → 비밀번호 설정/로그인/변경 → 파일 닫기/다시 열기를 검증하지만, server.NewManager/New 부팅 테스트는 아니다.
- `sqlite-foundation` CI는 PG 서비스 없이 세 OS에서 연결 race 검사와 settings/auth/traffic 테스트를 실행하고 테스트 skip을 실패로 처리한다.
- 로컬은 여전히 네트워크 DNS 제한으로 저장소 clone/Go 1.26.3 의존성 다운로드가 불가능하다. Go 소스 형식/구문을 확인했고 전체 Go 검사는 원격 CI에서 확인한다.
- 원격 검증 결과: 아직 확인 전. 성공 결과와 커밋/run ID를 확인한 뒤 위 하위 항목만 완료로 바꾼다.
- 다음 단위: 업무 SQLite의 전체 스키마/시드와 startup 호출 의존 이식, NewManager/New 오류 반환, 모델 프로필 저장/재실행. 초기화 오류를 무시하고 ready를 내보내지 않는다.
- neobrutal-ui는 필수 UI 기준으로 그대로 유지한다. 이번에는 UI/에이전트 기능/외부 테스트 대상/사용자 데이터는 변경하지 않는다.
'''
path.write_text(text, encoding='utf-8')
path = Path('docs/architecture.md')
text = path.read_text(encoding='utf-8')
text += '''

## 현재 구현된 SQLite 연결 경계

`internal/sqlitedb.Open(ctx, absolutePath)`은 기존 `traffic.Open`에서 사용한다.
파일 경로만 입력받고, URL의 Path와 고정 Query를 분리해 한글/공백/#/%/?를 처리한다.
Windows 드라이브 경로는 file URI로 바꾸며 UNC/장치 경로를 허용하지 않는다. 다른 OS의 네트워크 마운트를 자동 판별한다는 뜻은 아니다.
연결별 PRAGMA는 드라이버 DSN에 있고 WAL 전환은 열린 파일에서 확인한다. 부모 폴더는 소유자가 준비한다.
새 파일은 0600으로 만들고 기존 파일을 자르거나 손상 데이터를 초기화하지 않는다. Windows ACL이나 악성 로컬 사용자의 경로 경합까지 격리하는 API는 아니다.
호출자가 풀/트랜잭션/쓰기 조정/스키마를 소유한다. 현재 트래픽의 wmu와 트랜잭션 경계를 유지한다.
업무 SQLite 부팅과 writer/read pool 정책은 이 연결 함수를 사용하는 후속 M2.2에서 실제 호출자와 함께 검증해야 한다.
'''
path.write_text(text, encoding='utf-8')
path = Path('docs/sqlite-porting-map.md')
text = path.read_text(encoding='utf-8')
text += '''

## M2.2a 적용 후 변경된 접점

위 인벤토리는 고정 커밋 기준의 조사 기록이다. 현재 트래픽의 bare-DSN 분기는 제거하고 `internal/sqlitedb.Open`에 실제 연결했다.
설정 조회 오류를 무시하던 인증 핸들러는 오류를 전파한다. 최초 비밀번호는 충돌 시 덮어쓰지 않는 INSERT, 변경은 기존 해시가 일치할 때만 UPDATE한다.
SQLite fixture에서 실제 setting 메서드와 HTTP 인증 핸들러를 검증하며, 해당 fixture가 51개 업무 테이블이나 전체 부팅을 대체하지는 않는다.
다음 구현자는 `db/db.go`/전체 스키마/시드/프로필/서버 생성 의존을 이어서 이식한다. 이번 테스트를 전체 앱 SQLite 전환 완료로 읽지 않는다.
'''
path.write_text(text, encoding='utf-8')
