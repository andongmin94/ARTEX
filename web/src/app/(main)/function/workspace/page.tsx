"use client";

import * as React from "react";

import {
  DownloadIcon,
  FileIcon,
  FolderIcon,
  FolderPlusIcon,
  HardDriveIcon,
  RefreshCwIcon,
  SaveIcon,
  Trash2Icon,
  UploadIcon,
} from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Sheet, SheetContent, SheetFooter, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import type { WorkspaceEntry, WorkspaceFile } from "@/lib/types";
import { cn } from "@/lib/utils";

function fmtSize(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`;
  return `${(n / 1024 / 1024 / 1024).toFixed(1)} GB`;
}
function fmtTime(ms: number): string {
  return new Date(ms).toLocaleString("ko-KR", {
    year: "2-digit",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

type EditState = {
  file: WorkspaceFile;
  content: string;
  dirty: boolean;
  saving: boolean;
};

export default function WorkspacePage() {
  const [path, setPath] = React.useState("");
  const [entries, setEntries] = React.useState<WorkspaceEntry[]>([]);
  const [loading, setLoading] = React.useState(true);
  const [edit, setEdit] = React.useState<EditState | null>(null);
  const [mkdirOpen, setMkdirOpen] = React.useState(false);
  const [mkdirName, setMkdirName] = React.useState("");
  const uploadRef = React.useRef<HTMLInputElement>(null);

  const load = React.useCallback((p: string) => {
    setLoading(true);
    api
      .workspaceList(p)
      .then((r) => {
        setEntries(r.entries);
        setPath(r.path);
      })
      .catch((e) => toast.error(`디렉터리 읽기 실패: ${(e as Error).message}`))
      .finally(() => setLoading(false));
  }, []);

  React.useEffect(() => {
    load("");
  }, [load]);

  const crumbs = React.useMemo(() => {
    const parts = path ? path.split("/") : [];
    const acc: { name: string; path: string }[] = [{ name: "전체 파일", path: "" }];
    let cur = "";
    for (const part of parts) {
      cur = cur ? `${cur}/${part}` : part;
      acc.push({ name: part, path: cur });
    }
    return acc;
  }, [path]);

  const openFile = (e: WorkspaceEntry) => {
    api
      .workspaceRead(e.path)
      .then((f) => setEdit({ file: f, content: f.content ?? "", dirty: false, saving: false }))
      .catch((err) => toast.error(`파일 열기 실패: ${(err as Error).message}`));
  };

  const saveFile = () => {
    if (!edit) return;
    setEdit({ ...edit, saving: true });
    api
      .workspaceWrite(edit.file.path, edit.content)
      .then(() => {
        toast.success("저장되었습니다");
        setEdit((cur) => (cur ? { ...cur, dirty: false, saving: false } : cur));
        load(path);
      })
      .catch((err) => {
        toast.error(`저장 실패: ${(err as Error).message}`);
        setEdit((cur) => (cur ? { ...cur, saving: false } : cur));
      });
  };

  const del = (e: WorkspaceEntry) => {
    if (
      !window.confirm(
        `${e.dir ? "디렉터리" : "파일"} 「${e.name}」을(를) 삭제할까요?${e.dir ? "(하위 모든 내용 포함)" : ""}`,
      )
    )
      return;
    api
      .workspaceDelete(e.path)
      .then(() => {
        toast.success("삭제되었습니다");
        load(path);
      })
      .catch((err) => toast.error(`삭제 실패: ${(err as Error).message}`));
  };

  const doUpload = (files: FileList | null) => {
    if (!files || files.length === 0) return;
    api
      .workspaceUpload(path, Array.from(files))
      .then((r) => {
        toast.success(`파일 ${r.uploaded}개를 업로드했습니다`);
        load(path);
      })
      .catch((err) => toast.error(`업로드 실패: ${(err as Error).message}`))
      .finally(() => {
        if (uploadRef.current) uploadRef.current.value = "";
      });
  };

  const doMkdir = () => {
    const name = mkdirName.trim();
    if (!name) return;
    const target = path ? `${path}/${name}` : name;
    api
      .workspaceMkdir(target)
      .then(() => {
        toast.success("디렉터리를 생성했습니다");
        setMkdirOpen(false);
        setMkdirName("");
        load(path);
      })
      .catch((err) => toast.error(`생성 실패: ${(err as Error).message}`));
  };

  return (
    <div className="flex flex-col gap-4 md:gap-6">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="space-y-1">
          <h1 className="font-semibold text-xl tracking-tight">작업 공간</h1>
          <p className="text-muted-foreground text-sm">작업 파일을 탐색하고 업로드하거나 편집합니다.</p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={() => setMkdirOpen(true)}>
            <FolderPlusIcon /> 새 폴더
          </Button>
          <Button variant="outline" size="sm" onClick={() => uploadRef.current?.click()}>
            <UploadIcon /> 업로드
          </Button>
          <Button variant="ghost" size="icon" className="size-8" onClick={() => load(path)} aria-label="새로고침">
            <RefreshCwIcon className={cn("size-4", loading && "animate-spin")} />
          </Button>
          <input
            ref={uploadRef}
            type="file"
            multiple
            aria-label="파일 업로드"
            className="hidden"
            onChange={(e) => doUpload(e.target.files)}
          />
        </div>
      </div>

      <div className="space-y-3">
        <div className="flex items-center justify-between gap-3">
          <nav aria-label="현재 폴더" className="flex min-w-0 items-center gap-1 text-sm">
            <HardDriveIcon aria-hidden="true" className="text-muted-foreground mr-1 size-4 shrink-0" />
            {crumbs.map((c, i) => (
              <React.Fragment key={c.path}>
                {i > 0 && <span className="text-muted-foreground">/</span>}
                <button
                  type="button"
                  onClick={() => load(c.path)}
                  aria-current={i === crumbs.length - 1 ? "page" : undefined}
                  className={cn(
                    "max-w-[160px] truncate rounded px-1.5 py-0.5 hover:bg-accent",
                    i === crumbs.length - 1 ? "text-foreground font-medium" : "text-muted-foreground",
                  )}
                >
                  {c.name}
                </button>
              </React.Fragment>
            ))}
          </nav>
          <span className="shrink-0 text-muted-foreground text-xs tabular-nums" aria-live="polite">
            {loading ? "불러오는 중…" : `${entries.length}개 항목`}
          </span>
        </div>
        <div className="rounded-[var(--radius)] bg-card text-card-foreground shadow-raised">
          <Table aria-label="작업 파일" aria-busy={loading} className="table-fixed">
            <TableHeader>
              <TableRow>
                <TableHead className="px-4">이름</TableHead>
                <TableHead className="w-28 px-4 text-right">크기</TableHead>
                <TableHead className="w-44 px-4">수정 시각</TableHead>
                <TableHead className="w-24 px-4 text-right">작업</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {entries.length === 0 && (
                <TableRow>
                  <TableCell colSpan={4} className="text-muted-foreground py-10 text-center text-sm">
                    {loading ? "불러오는 중…" : "빈 디렉터리"}
                  </TableCell>
                </TableRow>
              )}
              {entries.map((e) => (
                <TableRow key={e.path}>
                  <TableCell className="px-4 py-3">
                    <button
                      type="button"
                      onClick={() => (e.dir ? load(e.path) : openFile(e))}
                      title={e.name}
                      className="flex w-full min-w-0 items-center gap-2 rounded-xs text-left outline-none hover:underline focus-visible:ring-2 focus-visible:ring-ring"
                    >
                      {e.dir ? (
                        <FolderIcon aria-hidden="true" className="size-4 shrink-0 text-primary-text" />
                      ) : (
                        <FileIcon aria-hidden="true" className="text-muted-foreground size-4 shrink-0" />
                      )}
                      <span className="min-w-0 truncate text-sm">{e.name}</span>
                    </button>
                  </TableCell>
                  <TableCell className="px-4 text-muted-foreground text-right text-xs tabular-nums">
                    {e.dir ? "—" : fmtSize(e.size)}
                  </TableCell>
                  <TableCell className="px-4 text-muted-foreground text-xs tabular-nums">{fmtTime(e.mtime)}</TableCell>
                  <TableCell className="px-4">
                    <div className="flex items-center justify-end gap-1">
                      {!e.dir && (
                        <Button
                          variant="ghost"
                          size="icon"
                          className="size-7"
                          title="다운로드"
                          aria-label={`${e.name} 다운로드`}
                          onClick={() =>
                            api
                              .workspaceDownload(e.path)
                              .catch((err) => toast.error(`다운로드 실패:${(err as Error).message}`))
                          }
                        >
                          <DownloadIcon className="size-3.5" />
                        </Button>
                      )}
                      <Button
                        variant="ghost"
                        size="icon"
                        className="text-destructive size-7"
                        title="삭제"
                        aria-label={`${e.name} 삭제`}
                        onClick={() => del(e)}
                      >
                        <Trash2Icon className="size-3.5" />
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      </div>

      {/* 文件查看 / 编辑 */}
      <Sheet open={edit !== null} onOpenChange={(o) => !o && setEdit(null)}>
        <SheetContent side="right" className="flex w-full flex-col gap-0 p-0 sm:max-w-2xl">
          {edit && (
            <>
              <SheetHeader className="border-b p-4">
                <SheetTitle className="flex items-center gap-2 truncate font-sans text-sm">
                  <FileIcon className="size-4 shrink-0" />
                  <span className="truncate" title={edit.file.path}>
                    {edit.file.path}
                  </span>
                </SheetTitle>
                <span className="text-muted-foreground text-xs">{fmtSize(edit.file.size)}</span>
              </SheetHeader>

              {edit.file.binary || edit.file.too_large ? (
                <div className="flex flex-1 flex-col items-center justify-center gap-3 p-8 text-center">
                  <p className="text-muted-foreground text-sm">
                    {edit.file.too_large
                      ? "파일이 너무 커서 온라인 미리보기/편집을 지원하지 않습니다."
                      : "바이너리 파일은 온라인 미리보기/편집을 지원하지 않습니다."}
                  </p>
                  <Button variant="outline" onClick={() => api.workspaceDownload(edit.file.path)}>
                    <DownloadIcon /> 파일 다운로드
                  </Button>
                </div>
              ) : (
                <>
                  <div className="min-h-0 flex-1 p-3">
                    <Textarea
                      aria-label="파일 내용"
                      value={edit.content}
                      onChange={(ev) => setEdit({ ...edit, content: ev.target.value, dirty: true })}
                      spellCheck={false}
                      className="h-full min-h-[50vh] resize-none font-sans text-xs leading-relaxed"
                    />
                  </div>
                  <SheetFooter className="flex-row items-center justify-between border-t p-3">
                    <span className="text-muted-foreground text-xs">
                      {edit.dirty ? "저장되지 않은 변경" : "동기화됨"}
                    </span>
                    <div className="flex gap-2">
                      <Button variant="outline" onClick={() => api.workspaceDownload(edit.file.path)}>
                        <DownloadIcon /> 다운로드
                      </Button>
                      <Button onClick={saveFile} disabled={!edit.dirty || edit.saving}>
                        <SaveIcon /> {edit.saving ? "저장 중…" : "저장"}
                      </Button>
                    </div>
                  </SheetFooter>
                </>
              )}
            </>
          )}
        </SheetContent>
      </Sheet>

      {/* 새 폴더 */}
      <Dialog open={mkdirOpen} onOpenChange={setMkdirOpen}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle>새 폴더</DialogTitle>
          </DialogHeader>
          <Input
            aria-label="폴더 이름"
            autoFocus
            value={mkdirName}
            onChange={(e) => setMkdirName(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && doMkdir()}
            placeholder="폴더 이름"
          />
          <DialogFooter>
            <Button variant="outline" onClick={() => setMkdirOpen(false)}>
              취소
            </Button>
            <Button onClick={doMkdir} disabled={!mkdirName.trim()}>
              생성
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
