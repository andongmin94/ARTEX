// One-time localization source transformation; not a runtime dependency.
package main

import (
 "bytes"
 "fmt"
 "go/ast"
 "go/format"
 "go/parser"
 "go/token"
 "os"
 "os/exec"
 "path/filepath"
 "sort"
 "strconv"
 "strings"
 "unicode"
)
const baseline = "325a07e9fdb3f06ff7dfdf70ef732cd1a0824d8e"
func must(e error) { if e != nil { panic(e) } }
func han(s string) bool { for _,r:=range s {if unicode.Is(unicode.Han,r){return true}};return false }
func gofiles(root string) []string {
 var paths []string
 must(filepath.WalkDir(root,func(p string,d os.DirEntry,e error)error{
  if e!=nil{return e};if d.IsDir(){if strings.HasPrefix(d.Name(),".")||d.Name()=="node_modules" {return filepath.SkipDir};return nil}
  if strings.HasSuffix(p,".go"){paths=append(paths,p)};return nil
 }))
 return paths
}
func sql(s string) string {
 pairs:=[]string{
  "'资产 #'","'자산 #'", "'未分类'","'미분류'",
  "'Agent 未保存复测结论，请查看会话后重新复测'","'에이전트가 재검증 결론을 저장하지 않았습니다. 세션을 확인한 뒤 다시 검증하세요'",
  "'服务重启，复测已中断，请重新发起'","'서비스 재시작으로 재검증이 중단되었습니다. 다시 시작하세요'",
  "'[模型]%'","'[모델]%'", "'服务重启，回答已中断'","'서비스 재시작으로 답변이 중단되었습니다'",
  "'上次归档进程异常退出，请手动重试'","'이전 보관 프로세스가 비정상 종료되었습니다. 다시 시도하세요'",
  "'意图在黑板中锚定该资产'","'블랙보드에서 의도가 이 자산에 연결됨'",
  "'任务创建时关联企业'","'작업 생성 시 연결한 기업'", "'任务创建时关联企业：'","'작업 생성 시 연결한 기업: '" ,
  "-- 已盖过章：不动","-- 이미 기록됨: 변경하지 않음",
  "'漏洞复测'","'취약점 재검증'", "'从漏洞详情手动启动，读取原证据并保存独立复测结论。'","'취약점 상세에서 직접 시작하여 기존 증거를 읽고 독립적인 재검증 결론을 저장합니다.'",
  "'内置默认'","'기본 제공 값'",
 }
 return strings.NewReplacer(pairs...).Replace(s)
}
func protocol(s string) string {
 s=strings.NewReplacer("[模型]","[모델]","【上传文件（绝对路径）】","【업로드 파일(절대 경로)】",
  "实际操作：","실제 작업:","；成功后的后果：","; 성공 시 결과:","；命中规则：","; 적용 규칙:",
  "@[漏洞#","@[취약점#","@[资产#","@[자산#","@[企业#","@[기업#","@[接口#","@[엔드포인트#","@[应用#","@[애플리케이션#","@[域名#","@[도메인#","@[子域名#","@[서브도메인#","@[服务#","@[서비스#",
 ).Replace(s)
 return s
}
func boundary(original, translated string) string {
 left:=original[:len(original)-len(strings.TrimLeftFunc(original,unicode.IsSpace))]
 right:=original[len(strings.TrimRightFunc(original,unicode.IsSpace)):]
 return left+strings.TrimSpace(translated)+right
}
type edit struct{start,end int;text string}
func main(){
 tmp,e:=os.MkdirTemp("","artex-go-original-");must(e);defer os.RemoveAll(tmp)
 archive,e:=exec.Command("git","archive","--format=tar",baseline).Output();must(e)
 unpack:=exec.Command("tar","-xf","-","-C",tmp);unpack.Stdin=bytes.NewReader(archive);must(unpack.Run())
 originals:=[]string{};seen:=map[string]bool{}
 for _,p:=range gofiles(tmp){
  if strings.HasSuffix(p,"_test.go"){continue}
  f,e:=parser.ParseFile(token.NewFileSet(),p,nil,0);must(e)
  ast.Inspect(f,func(n ast.Node)bool{v,ok:=n.(*ast.BasicLit);if !ok||v.Kind!=token.STRING{return true};s,e:=strconv.Unquote(v.Value);must(e);if han(s)&&!seen[s]{seen[s]=true;originals=append(originals,s)};return true})
 }
 if len(originals)!=1591{panic(fmt.Sprintf("Unexpected baseline count: %d",len(originals)))}
 translations:=map[string]string{};ids:=map[int]bool{}
 paths,e:=filepath.Glob(".localization/go-*.tsv");must(e)
 for _,p:=range paths{
  b,e:=os.ReadFile(p);must(e)
  for _,line:=range strings.Split(strings.TrimSuffix(string(b),"\n"),"\n"){
   a,value,ok:=strings.Cut(line,"\t");if !ok{panic("Invalid translation line in "+p)}
   id,e:=strconv.Atoi(a);must(e);if id<0||id>=len(originals)||ids[id]{panic("Invalid/duplicate translation ID: "+a)}
   if id!=897 {value=strings.ReplaceAll(value,`\n`,"\n")}
   if strings.TrimSpace(value)==""||han(value){panic("Untranslated entry: "+a)}
   ids[id]=true;translations[originals[id]]=boundary(originals[id],value)
  }
 }
 // External provider messages and ICP registration syntax are input data, not UI.
 excluded:=map[int]bool{110:true,111:true,112:true,113:true,450:true}
 for id,s:=range originals{
  if ids[id]||excluded[id]{continue}
  next:=sql(s)
  if next==s||han(next){panic(fmt.Sprintf("Missing translation ID %d: %.160s",id,s))}
  ids[id]=true;translations[s]=next
 }
 files,changes:=0,0
 for _,p:=range gofiles("."){
  b,e:=os.ReadFile(p);must(e);fs:=token.NewFileSet();f,e:=parser.ParseFile(fs,p,b,parser.ParseComments);must(e);var edits []edit
  ast.Inspect(f,func(n ast.Node)bool{
   v,ok:=n.(*ast.BasicLit);if !ok||v.Kind!=token.STRING{return true};s,e:=strconv.Unquote(v.Value);must(e)
   out:=s
   if t,ok:=translations[s];ok{out=t}else if han(s){out=sql(s)}
   out=protocol(out)
   if out!=s {raw:=strconv.Quote(out);if strings.HasPrefix(v.Value,"`")&&!strings.ContainsRune(out,'`'){raw="`"+out+"`"};edits=append(edits,edit{fs.Position(v.Pos()).Offset,fs.Position(v.End()).Offset,raw})}
   return true
  })
  if len(edits)==0{continue}
  sort.Slice(edits,func(i,j int)bool{return edits[i].start>edits[j].start})
  for _,x:=range edits{b=append(append(append([]byte{},b[:x.start]...),[]byte(x.text)...),b[x.end:]...)}
  formatted,e:=format.Source(b);must(e);must(os.WriteFile(p,formatted,0644));files++;changes+=len(edits)
 }
 residual:=0
 for _,p:=range gofiles("."){
  if strings.HasSuffix(p,"_test.go"){continue}
  fs:=token.NewFileSet();f,e:=parser.ParseFile(fs,p,nil,0);must(e)
  ast.Inspect(f,func(n ast.Node)bool{v,ok:=n.(*ast.BasicLit);if !ok||v.Kind!=token.STRING{return true};s,e:=strconv.Unquote(v.Value);must(e);if !han(s){return true};for id:=range excluded{if s==originals[id]{return true}};fmt.Printf("RESIDUAL %s:%d %q\n",p,fs.Position(v.Pos()).Line,s);residual++;return true})
 }
 if residual!=0{panic(fmt.Sprintf("%d untranslated backend strings",residual))}
 fmt.Printf("Translated %d unique backend literals across %d files (%d occurrences); residual=0, external-input exceptions=%d\n",len(ids),files,changes,len(excluded))
}
