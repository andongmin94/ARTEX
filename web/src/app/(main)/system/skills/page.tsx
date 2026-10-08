"use client";

import * as React from "react";
import { toast } from "sonner";
import {
  ChevronRightIcon,
  FolderIcon,
  FolderOpenIcon,
  FolderPlusIcon,
  FileTextIcon,
  FilePlusIcon,
  PlusIcon,
  Trash2Icon,
  UploadIcon,
  AlertTriangleIcon,
} from "lucide-react";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Checkbox } from "@/components/ui/checkbox";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Badge } from "@/components/ui/badge";
import {
  Sheet,
  SheetContent,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { cn } from "@/lib/utils";
import { api } from "@/lib/api";
import { ApiError } from "@/lib/api-error";
import type { Agent, SkillItem, MCPServer, SkillCall, MissingSkill } from "@/lib/types";

function fmtTime(ts?: string) {
  if (!ts) return "호출 기록 없음";
  return new Date(ts).toLocaleString("ko-KR", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

// ── Tree node ──────────────────────────────────────────────────────────────
interface TreeNode {
  name: string;
  path: string;   // relative to skill root, dirs WITHOUT trailing slash
  type: "file" | "dir";
  children: TreeNode[];
}

// Backend returns dirs as "scripts/" (trailing slash) and files as "scripts/a.py".
function buildTree(entries: string[]): TreeNode[] {
  const root: TreeNode[] = [];
  const dirMap = new Map<string, TreeNode>();

  function ensureDir(dirPath: string, parentNodes: TreeNode[]): TreeNode[] {
    if (dirMap.has(dirPath)) return dirMap.get(dirPath)!.children;
    const parts = dirPath.split("/");
    let nodes = root;
    let cur = "";
    for (const part of parts) {
      cur = cur ? `${cur}/${part}` : part;
      if (!dirMap.has(cur)) {
        const d: TreeNode = { name: part, path: cur, type: "dir", children: [] };
        nodes.push(d);
        dirMap.set(cur, d);
      }
      nodes = dirMap.get(cur)!.children;
    }
    return nodes;
  }

  for (const entry of [...entries].sort()) {
    if (entry.endsWith("/")) {
      // Explicit directory entry — ensure the node exists (may already be created)
      ensureDir(entry.slice(0, -1), root);
    } else {
      // File — ensure parent dirs exist, then push file node
      const parts = entry.split("/");
      let nodes = root;
      let cur = "";
      for (let i = 0; i < parts.length - 1; i++) {
        cur = cur ? `${cur}/${parts[i]}` : parts[i];
        if (!dirMap.has(cur)) {
          const d: TreeNode = { name: parts[i], path: cur, type: "dir", children: [] };
          nodes.push(d);
          dirMap.set(cur, d);
        }
        nodes = dirMap.get(cur)!.children;
      }
      const fname = parts[parts.length - 1];
      if (fname) nodes.push({ name: fname, path: entry, type: "file", children: [] });
    }
  }
  sortNodes(root);
  return root;
}

// sortNodes orders every level like a file explorer: directories before files,
// then case-insensitive by name. Recurses into children.
function sortNodes(nodes: TreeNode[]): void {
  nodes.sort((a, b) => {
    if (a.type !== b.type) return a.type === "dir" ? -1 : 1;
    return a.name.localeCompare(b.name, undefined, { sensitivity: "base" });
  });
  for (const n of nodes) {
    if (n.children.length > 0) sortNodes(n.children);
  }
}

// ── State types ───────────────────────────────────────────────────────────
type Selected =
  | { skill: string; path: null }
  | { skill: string; path: string };

type Creating = {
  skill: string;
  inDir: string;
  kind: "file" | "dir";
} | null;

type PendingDelete =
  | { kind: "skill"; skill: string }
  | { kind: "file"; skill: string; path: string }
  | { kind: "dir"; skill: string; path: string }
  | null;

// ── Overview (empty-state) ──────────────────────────────────────────────────
// Shown when nothing is selected: a library-wide snapshot from data already loaded
// (the skill list carries per-skill usage; missing is fetched alongside). No extra
// requests — this is pure aggregation over props.
function SkillsOverview({
  skills,
  missing,
  onSelect,
}: {
  skills: SkillItem[];
  missing: MissingSkill[];
  onSelect: (name: string) => void;
}) {
  const agg = React.useMemo(() => {
    const totalCalls = skills.reduce((n, s) => n + s.calls, 0);
    const used = skills.filter((s) => s.calls > 0);
    const ranked = [...used].sort((a, b) => b.calls - a.calls);
    const neverUsed = skills.filter((s) => s.calls === 0);
    const recent = skills
      .filter((s) => s.last_used)
      .sort((a, b) => (a.last_used! < b.last_used! ? 1 : -1))
      .slice(0, 6);
    const missingCalls = missing.reduce((n, m) => n + m.calls, 0);
    return {
      totalCalls,
      usedCount: used.length,
      ranked,
      topCalls: ranked[0]?.calls ?? 0,
      neverUsed,
      recent,
      missingCalls,
    };
  }, [skills, missing]);

  if (skills.length === 0) {
    return (
      <div className="flex h-full items-center justify-center">
        <p className="text-sm text-muted-foreground">스킬이 없습니다. 왼쪽의 「새로 만들기」 또는 「압축 파일 업로드」로 시작하세요</p>
      </div>
    );
  }

  const stats: { label: string; value: React.ReactNode; hint?: string }[] = [
    { label: "전체 스킬 수", value: skills.length, hint: `${agg.usedCount} 개 사용됨` },
    { label: "누적 호출", value: agg.totalCalls },
    { label: "미사용", value: agg.neverUsed.length, hint: agg.neverUsed.length > 0 ? "어떤 에이전트도 불러온 적이 없습니다" : "모두 사용됨" },
    { label: "찾지 못한 스킬 호출", value: agg.missingCalls, hint: missing.length > 0 ? `${missing.length} 개의 존재하지 않는 스킬` : "없음" },
  ];

  return (
    <div className="mx-auto w-full max-w-3xl space-y-6">
      <div>
        <h2 className="text-base font-semibold">스킬 라이브러리 개요</h2>
        <p className="text-muted-foreground text-sm">왼쪽에서 스킬을 선택하여 상세 정보와 호출 기록을 보거나 전체 사용 현황을 확인하세요.</p>
      </div>

      {/* 指标卡 */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        {stats.map((s) => (
          <div key={s.label} className="rounded-lg border p-3">
            <p className="text-2xl font-semibold tabular-nums">{s.value}</p>
            <p className="text-xs font-medium">{s.label}</p>
            {s.hint && <p className="text-muted-foreground mt-0.5 text-[11px]">{s.hint}</p>}
          </div>
        ))}
      </div>

      {/* 调用排行 */}
      <div className="space-y-2">
        <Label className="text-xs text-muted-foreground">호출 순위</Label>
        {agg.ranked.length === 0 ? (
          <p className="text-muted-foreground rounded-lg border border-dashed p-4 text-center text-xs">
            스킬 호출 기록이 없습니다.
          </p>
        ) : (
          <div className="space-y-1.5">
            {agg.ranked.slice(0, 8).map((s) => (
              <button
                key={s.name}
                type="button"
                onClick={() => onSelect(s.name)}
                className="group flex w-full items-center gap-3 rounded-md px-2 py-1.5 text-left hover:bg-muted"
              >
                <span className="w-40 shrink-0 truncate font-sans text-xs" title={s.name}>{s.name}</span>
                <span className="relative h-2 flex-1 overflow-hidden rounded-full bg-muted">
                  <span
                    className="absolute inset-y-0 left-0 rounded-full bg-primary/70"
                    style={{ width: `${agg.topCalls > 0 ? (s.calls / agg.topCalls) * 100 : 0}%` }}
                  />
                </span>
                <span className="w-16 shrink-0 text-right text-xs tabular-nums text-muted-foreground">
                  {s.calls} 회
                </span>
              </button>
            ))}
          </div>
        )}
      </div>

      <div className="grid gap-6 sm:grid-cols-2">
        {/* 最近调用 */}
        <div className="space-y-2">
          <Label className="text-xs text-muted-foreground">최근 호출</Label>
          {agg.recent.length === 0 ? (
            <p className="text-muted-foreground text-xs">기록이 없습니다.</p>
          ) : (
            <div className="space-y-1">
              {agg.recent.map((s) => (
                <button
                  key={s.name}
                  type="button"
                  onClick={() => onSelect(s.name)}
                  className="flex w-full items-center gap-2 rounded px-1.5 py-1 text-left hover:bg-muted"
                >
                  <span className="min-w-0 flex-1 truncate font-sans text-xs" title={s.name}>{s.name}</span>
                  <span className="shrink-0 text-[11px] tabular-nums text-muted-foreground">{fmtTime(s.last_used)}</span>
                </button>
              ))}
            </div>
          )}
        </div>

        {/* 未使用（可清理 / 需曝光） */}
        <div className="space-y-2">
          <Label className="text-xs text-muted-foreground">
            사용하지 않은 스킬
            {agg.neverUsed.length > 0 && <span className="ml-1 font-normal">（{agg.neverUsed.length}）</span>}
          </Label>
          {agg.neverUsed.length === 0 ? (
            <p className="text-muted-foreground text-xs">모든 스킬을 한 번 이상 호출했습니다.</p>
          ) : (
            <div className="flex flex-wrap gap-1.5">
              {agg.neverUsed.map((s) => (
                <button
                  key={s.name}
                  type="button"
                  onClick={() => onSelect(s.name)}
                  title={s.name}
                >
                  <Badge variant="outline" className="max-w-[12rem] cursor-pointer truncate font-sans text-xs font-normal hover:bg-muted">
                    {s.name}
                  </Badge>
                </button>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

// ── Page ──────────────────────────────────────────────────────────────────
export default function SkillsPage() {
  const [skills, setSkills] = React.useState<SkillItem[]>([]);
  const [agents, setAgents] = React.useState<Agent[]>([]);
  const [selected, setSelected] = React.useState<Selected | null>(null);
  const [visibility, setVisibility] = React.useState<Record<string, string[]>>({});
  const [expanded, setExpanded] = React.useState<Set<string>>(new Set());

  // file editor
  const [fileContent, setFileContent] = React.useState("");
  const [fileLoading, setFileLoading] = React.useState(false);
  const [dirty, setDirty] = React.useState(false);
  const [saving, setSaving] = React.useState(false);

  // inline create — using a ref to avoid stale closure in onBlur
  const [creating, setCreating] = React.useState<Creating>(null);
  const [newEntryName, setNewEntryName] = React.useState("");
  const inlineRef = React.useRef<HTMLInputElement>(null);
  // cancelRef prevents onBlur from committing when Escape was pressed
  const cancelRef = React.useRef(false);

  // new skill dialog
  const [newOpen, setNewOpen] = React.useState(false);
  const [newName, setNewName] = React.useState("");
  const [newDesc, setNewDesc] = React.useState("");
  const [newLicense, setNewLicense] = React.useState("");
  const [newCompat, setNewCompat] = React.useState("");
  const [newInst, setNewInst] = React.useState("");
  const [newMcps, setNewMcps] = React.useState<string[]>([]);
  const [newVisibility, setNewVisibility] = React.useState<string[]>([]);
  const [mcpOptions, setMcpOptions] = React.useState<MCPServer[]>([]);
  const [creatingSkill, setCreatingSkill] = React.useState(false);
  const [uploading, setUploading] = React.useState(false);
  const uploadRef = React.useRef<HTMLInputElement>(null);

  // skill detail — MCP edit state (optimistic, rolls back on error)
  const [detailMcps, setDetailMcps] = React.useState<string[]>([]);

  // delete confirmation
  const [pendingDelete, setPendingDelete] = React.useState<PendingDelete>(null);
  const [deleting, setDeleting] = React.useState(false);

  // 调用统计：列表页的次数/最近调用随 api.skills() 一起回来；选中某个 skill 时再拉它的
  // 最近调用明细。missing = 被点名但不存在的 skill（想用但没有）。
  const [usageCalls, setUsageCalls] = React.useState<SkillCall[]>([]);
  const [usageLoading, setUsageLoading] = React.useState(false);
  const [missing, setMissing] = React.useState<MissingSkill[]>([]);

  // ── Data ──────────────────────────────────────────────────────────────────
  const load = React.useCallback(() => {
    api.agents().then(setAgents).catch(() => {});
    api.mcpServers().then(setMcpOptions).catch(() => {});
    api.missingSkills().then(setMissing).catch(() => {});
    api.skills().then((ss) => {
      setSkills(ss);
      ss.forEach((s) =>
        api.skillVisibility(s.name)
          .then((ids) => setVisibility((v) => ({ ...v, [s.name]: ids })))
          .catch(() => {}),
      );
    }).catch(() => {});
  }, []);

  React.useEffect(() => { load(); }, [load]);

  // ── Upload a .zip skill ───────────────────────────────────────────────────
  async function uploadZip(file: File, overwrite = false) {
    setUploading(true);
    try {
      const r = await api.uploadSkill(file, overwrite);
      toast.success(`스킬 설치 완료:${r.name}（${r.files} 개 파일)`);
      load();
    } catch (e) {
      const msg = (e as Error).message;
      // offer overwrite when the skill already exists
      if (!overwrite && e instanceof ApiError && e.status === 409) {
        if (window.confirm(`${msg}

동일한 이름의 스킬을 덮어쓸까요?`)) {
          await uploadZip(file, true);
          return;
        }
      } else {
        toast.error("업로드 실패:" + msg);
      }
    } finally {
      setUploading(false);
    }
  }
  function onUploadPick(e: React.ChangeEvent<HTMLInputElement>) {
    const f = e.target.files?.[0];
    e.target.value = ""; // reset so picking the same file again re-fires
    if (f) uploadZip(f);
  }

  React.useEffect(() => {
    if (creating) {
      // Small timeout so the element is actually in the DOM before focusing
      setTimeout(() => inlineRef.current?.focus(), 20);
    }
  }, [creating]);

  // Sync detail-panel MCP state when the selected skill changes.
  React.useEffect(() => {
    if (!selected || selected.path !== null) return;
    const sk = skills.find((s) => s.name === selected.skill);
    setDetailMcps(sk?.mcps ?? []);
  }, [selected, skills]);

  // Recent calls for the selected skill (detail panel only).
  React.useEffect(() => {
    if (!selected || selected.path !== null) { setUsageCalls([]); return; }
    const name = selected.skill;
    setUsageLoading(true);
    api.skillUsage(name, 20)
      .then((calls) => setUsageCalls(calls))
      .catch(() => setUsageCalls([]))
      .finally(() => setUsageLoading(false));
  }, [selected]);

  React.useEffect(() => {
    if (!selected || selected.path === null) { setFileContent(""); setDirty(false); return; }
    setFileLoading(true);
    api.readSkillFile(selected.skill, selected.path)
      .then((c) => { setFileContent(c); setDirty(false); })
      .catch(() => toast.error("파일 읽기 실패"))
      .finally(() => setFileLoading(false));
  }, [selected]);

  // ── Expand helpers ────────────────────────────────────────────────────────
  function toggleExpanded(key: string) {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key); else next.add(key);
      return next;
    });
  }
  function ensureExpanded(skill: string, dirPath: string) {
    setExpanded((prev) => {
      const next = new Set(prev);
      next.add(skill);
      if (dirPath) {
        let cur = "";
        for (const part of dirPath.split("/")) {
          cur = cur ? `${cur}/${part}` : part;
          next.add(`${skill}:${cur}`);
        }
      }
      return next;
    });
  }

  // ── Inline create ─────────────────────────────────────────────────────────
  function startCreate(skill: string, inDir: string, kind: "file" | "dir") {
    cancelRef.current = false;
    ensureExpanded(skill, inDir);
    setCreating({ skill, inDir, kind });
    setNewEntryName("");
  }

  // Use a ref snapshot so commitCreate reads the latest creating value
  // without depending on potentially stale closure state.
  const creatingRef = React.useRef<Creating>(null);
  React.useEffect(() => { creatingRef.current = creating; }, [creating]);
  const newEntryRef = React.useRef("");
  React.useEffect(() => { newEntryRef.current = newEntryName; }, [newEntryName]);

  async function commitCreate() {
    if (cancelRef.current) { cancelRef.current = false; return; }
    const c = creatingRef.current;
    const name = newEntryRef.current.trim();
    setCreating(null);
    setNewEntryName("");
    if (!c || !name) return;

    const fullPath = c.inDir ? `${c.inDir}/${name}` : name;
    try {
      if (c.kind === "dir") {
        await api.createSkillDir(c.skill, fullPath);
        toast.success(`폴더 생성 완료:${fullPath}`);
        ensureExpanded(c.skill, fullPath);
      } else {
        await api.writeSkillFile(c.skill, fullPath, "");
        toast.success(`파일 생성 완료:${fullPath}`);
        setSelected({ skill: c.skill, path: fullPath });
        ensureExpanded(c.skill, c.inDir);
      }
      load();
    } catch (e) {
      toast.error("생성 실패:" + (e as Error).message);
    }
  }

  function cancelCreate() {
    cancelRef.current = true;
    setCreating(null);
    setNewEntryName("");
  }

  async function deletePath(skill: string, path: string) {
    try {
      await api.deleteSkillPath(skill, path);
      toast.success(`삭제됨:${path}`);
      if (selected?.skill === skill && selected.path === path) setSelected(null);
      load();
    } catch (e) {
      toast.error("삭제 실패:" + (e as Error).message);
    }
  }

  async function deleteSkill(name: string) {
    try {
      await api.deleteSkill(name);
      toast.success(`스킬 삭제 완료:${name}`);
      if (selected?.skill === name) setSelected(null);
      load();
    } catch (e) {
      toast.error("삭제 실패:" + (e as Error).message);
    }
  }

  async function runPendingDelete() {
    const p = pendingDelete;
    if (!p) return;
    setDeleting(true);
    try {
      if (p.kind === "skill") await deleteSkill(p.skill);
      else await deletePath(p.skill, p.path);
    } finally {
      setDeleting(false);
      setPendingDelete(null);
    }
  }

  async function saveFile() {
    if (!selected || selected.path === null) return;
    setSaving(true);
    try {
      await api.writeSkillFile(selected.skill, selected.path, fileContent);
      toast.success("저장됨");
      setDirty(false);
    } catch (e) {
      toast.error("저장 실패:" + (e as Error).message);
    } finally { setSaving(false); }
  }

  async function toggleSkillMcp(skillName: string, mcpName: string, mcpOn: boolean) {
    const next = mcpOn
      ? [...detailMcps, mcpName]
      : detailMcps.filter((n) => n !== mcpName);
    setDetailMcps(next);
    try {
      await api.updateSkillMeta(skillName, { mcps: next });
      toast.success(`${mcpOn ? "연결" : "연결 해제"}「${mcpName}」`);
      load();
    } catch (e) {
      // roll back on error
      setDetailMcps(detailMcps);
      toast.error("작업 실패:" + (e as Error).message);
    }
  }

  async function toggleVisibility(skillName: string, agentId: string, agentName: string) {
    const on = (visibility[skillName] ?? []).includes(agentId);
    try {
      await api.toggleSkillVisibility(agentId, skillName, !on);
      toast.success(`${on ? "취소" : "허용"}「${agentName}」 접근 권한`);
      const ids = await api.skillVisibility(skillName);
      setVisibility((v) => ({ ...v, [skillName]: ids }));
    } catch (e) {
      toast.error("작업 실패:" + (e as Error).message);
    }
  }

  async function createNewSkill() {
    if (!newName.trim()) { toast.error("name을 입력하세요"); return; }
    if (!newDesc.trim()) { toast.error("description은 필수입니다"); return; }
    setCreatingSkill(true);
    try {
      const name = newName.trim();
      await api.createSkill({
        name, description: newDesc.trim(),
        license: newLicense.trim() || undefined,
        compatibility: newCompat.trim() || undefined,
        mcps: newMcps.length ? newMcps : undefined,
        instructions: newInst.trim() || undefined,
      });
      // apply initial visibility (fire-and-forget per agent; best-effort)
      await Promise.all(newVisibility.map((id) => api.toggleSkillVisibility(id, name, true)));
      toast.success("스킬을 생성했습니다");
      setNewOpen(false);
      setNewName(""); setNewDesc(""); setNewLicense(""); setNewCompat(""); setNewInst("");
      setNewMcps([]); setNewVisibility([]);
      load();
    } catch (e) {
      toast.error("생성 실패:" + (e as Error).message);
    } finally { setCreatingSkill(false); }
  }

  // ── Inline input JSX helper (NOT a React component — avoids remount on re-render) ──
  // Defined as a plain function returning JSX so React never sees a new component type.
  function inlineInputJSX(indent: number) {
    return (
      <div
        key="__inline_create__"
        className="flex items-center gap-1 py-0.5 pr-2"
        style={{ paddingLeft: indent }}
      >
        {creating?.kind === "dir"
          ? <FolderIcon className="size-3.5 shrink-0 text-amber-500" />
          : <FileTextIcon className="size-3.5 shrink-0 text-muted-foreground" />
        }
        <Input
          ref={inlineRef}
          className="h-6 flex-1 px-1 py-0 font-sans text-xs"
          placeholder={creating?.kind === "dir" ? "folder-name" : "filename.py"}
          value={newEntryName}
          onChange={(e) => setNewEntryName(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") { cancelRef.current = false; void commitCreate(); }
            if (e.key === "Escape") cancelCreate();
          }}
          onBlur={() => { void commitCreate(); }}
        />
      </div>
    );
  }

  // ── Recursive tree renderer ───────────────────────────────────────────────
  function renderTree(nodes: TreeNode[], skill: string, depth: number): React.ReactNode {
    // depth+1: the skill root sits at 8px (px-2); its children indent one step
    // further in so the tree reads as nested under the skill folder.
    const baseIndent = 8 + (depth + 1) * 14;
    return nodes.map((node) => {
      if (node.type === "dir") {
        const key = `${skill}:${node.path}`;
        const open = expanded.has(key);
        return (
          <div key={node.path}>
            <div
              className="group relative flex cursor-pointer select-none items-center gap-1 rounded py-0.5 pr-1 text-sm hover:bg-muted"
              style={{ paddingLeft: baseIndent }}
              onClick={() => toggleExpanded(key)}
            >
              <ChevronRightIcon className={cn("size-3.5 shrink-0 transition-transform", open && "rotate-90")} />
              {open
                ? <FolderOpenIcon className="size-3.5 shrink-0 text-amber-500" />
                : <FolderIcon className="size-3.5 shrink-0 text-amber-500" />
              }
              <span className="min-w-0 flex-1 truncate" title={node.path}>{node.name}</span>
              {/* Absolute so a long name can never push the actions out of view */}
              <span className="absolute inset-y-0 right-1 hidden items-center gap-0.5 rounded bg-muted pl-1 group-hover:flex">
                <Button size="icon" variant="ghost" className="size-5" title="새 파일"
                  onClick={(e) => { e.stopPropagation(); startCreate(skill, node.path, "file"); }}>
                  <FilePlusIcon className="size-3 text-muted-foreground" />
                </Button>
                <Button size="icon" variant="ghost" className="size-5" title="새 폴더"
                  onClick={(e) => { e.stopPropagation(); startCreate(skill, node.path, "dir"); }}>
                  <FolderPlusIcon className="size-3 text-muted-foreground" />
                </Button>
                <Button size="icon" variant="ghost" className="size-5" title="폴더 삭제"
                  onClick={(e) => { e.stopPropagation(); setPendingDelete({ kind: "dir", skill, path: node.path }); }}>
                  <Trash2Icon className="size-3 text-destructive" />
                </Button>
              </span>
            </div>
            {open && (
              <>
                {renderTree(node.children, skill, depth + 1)}
                {creating?.skill === skill && creating.inDir === node.path &&
                  inlineInputJSX(baseIndent + 14)}
              </>
            )}
          </div>
        );
      }

      // file node
      const isSelected = selected?.skill === skill && selected.path === node.path;
      return (
        <div
          key={node.path}
          className={cn(
            "group relative flex cursor-pointer select-none items-center gap-1 rounded py-0.5 pr-1 text-sm",
            isSelected ? "bg-accent text-accent-foreground" : "hover:bg-muted",
          )}
          style={{ paddingLeft: baseIndent + 16 }}
          onClick={() => setSelected({ skill, path: node.path })}
        >
          <FileTextIcon className="size-3.5 shrink-0 text-muted-foreground" />
          <span className="min-w-0 flex-1 truncate font-sans text-xs" title={node.path}>{node.name}</span>
          <span className={cn(
            "absolute inset-y-0 right-1 hidden items-center rounded pl-1 group-hover:flex",
            isSelected ? "bg-accent" : "bg-muted",
          )}>
            <Button size="icon" variant="ghost" className="size-5"
              title="파일 삭제"
              onClick={(e) => { e.stopPropagation(); setPendingDelete({ kind: "file", skill, path: node.path }); }}>
              <Trash2Icon className="size-3 text-destructive" />
            </Button>
          </span>
        </div>
      );
    });
  }

  const selectedSkill = selected
    ? skills.find((s) => s.name === selected.skill) ?? null
    : null;

  return (
    <div data-content-padding="false" className="flex flex-1 flex-col overflow-hidden">
      <div className="flex items-center gap-3 border-b px-4 py-2.5 lg:px-6">
        <div className="flex flex-col gap-0.5">
          <h1 className="text-sm font-semibold leading-tight">Skill</h1>
          <p className="text-muted-foreground text-xs">스킬 라이브러리 · agentskills.io 규격 · 에이전트별 접근 권한</p>
        </div>
        {/* 缺口清单：agent 点名调用、但库里没有的 skill —— 直接是该补什么的依据。 */}
        {missing.length > 0 && (
          <Popover>
            <PopoverTrigger asChild>
              <Button size="sm" variant="outline" className="ml-auto">
                <AlertTriangleIcon className="size-3.5 text-amber-500" />
                {missing.length} 건의 미발견 스킬 호출
              </Button>
            </PopoverTrigger>
            <PopoverContent align="end" className="w-80">
              <p className="mb-2 text-xs text-muted-foreground">
                에이전트가 호출했으나 라이브러리에 없는 스킬입니다. 요청 횟수순으로 표시합니다.
              </p>
              <div className="space-y-1">
                {missing.map((m) => (
                  <div key={m.skill} className="flex items-center gap-2 text-sm">
                    <code className="min-w-0 flex-1 truncate font-sans text-xs" title={m.skill}>{m.skill}</code>
                    <span className="shrink-0 text-xs tabular-nums text-muted-foreground">{m.calls} 회</span>
                    <span className="shrink-0 text-xs text-muted-foreground">{fmtTime(m.last_used)}</span>
                  </div>
                ))}
              </div>
            </PopoverContent>
          </Popover>
        )}
      </div>
      <div className="flex flex-1 overflow-hidden">
        {/* ── 左侧文件树 ── */}
        <div className="flex w-64 shrink-0 flex-col border-r">
          <div className="flex flex-col gap-2 border-b p-2">
            <Button size="sm" variant="outline" className="w-full" onClick={() => setNewOpen(true)}>
              <PlusIcon className="size-3.5" />새 스킬
            </Button>
            <input
              ref={uploadRef}
              type="file"
              accept=".zip,application/zip"
              className="hidden"
              onChange={onUploadPick}
            />
            <Button
              size="sm"
              variant="outline"
              className="w-full"
              disabled={uploading}
              onClick={() => uploadRef.current?.click()}
              title="SKILL.md가 포함된 .zip 압축 파일 업로드"
            >
              <UploadIcon className="size-3.5" />
              {uploading ? "업로드 중…" : "압축 파일 업로드"}
            </Button>
          </div>
          {/* Radix viewport wraps children in a display:table div that grows with
              content — force it to block so long names truncate instead of
              widening the rows past the sidebar. */}
          <ScrollArea className="flex-1 [&>[data-slot=scroll-area-viewport]>div]:!block">
            <div className="p-1">
              {skills.map((s) => {
                const isOpen = expanded.has(s.name);
                const tree = buildTree(s.files);
                const isSkillSelected = selected?.skill === s.name && selected.path === null;
                return (
                  <div key={s.name}>
                    {/* skill 根节点 */}
                    <div
                      className={cn(
                        "group relative flex cursor-pointer select-none items-center gap-1 rounded px-2 py-1 text-sm",
                        isSkillSelected ? "bg-accent text-accent-foreground" : "hover:bg-muted",
                      )}
                      onClick={() => {
                        toggleExpanded(s.name);
                        setSelected({ skill: s.name, path: null });
                      }}
                    >
                      <ChevronRightIcon className={cn("size-3.5 shrink-0 transition-transform", isOpen && "rotate-90")} />
                      {isOpen
                        ? <FolderOpenIcon className="size-3.5 shrink-0 text-blue-500" />
                        : <FolderIcon className="size-3.5 shrink-0 text-blue-500" />
                      }
                      <span className="min-w-0 flex-1 truncate font-semibold" title={s.name}>{s.name}</span>
                      {s.calls > 0 && (
                        <span
                          className="shrink-0 rounded bg-muted px-1 text-[10px] tabular-nums text-muted-foreground"
                          title={`호출 횟수: ${s.calls} 회 · 최근 ${fmtTime(s.last_used)}`}
                        >
                          {s.calls}
                        </span>
                      )}
                      <span className={cn(
                        "absolute inset-y-0 right-1 hidden items-center gap-0.5 rounded pl-1 group-hover:flex",
                        isSkillSelected ? "bg-accent" : "bg-muted",
                      )}>
                        <Button size="icon" variant="ghost" className="size-5" title="새 파일"
                          onClick={(e) => { e.stopPropagation(); startCreate(s.name, "", "file"); }}>
                          <FilePlusIcon className="size-3 text-muted-foreground" />
                        </Button>
                        <Button size="icon" variant="ghost" className="size-5" title="새 폴더"
                          onClick={(e) => { e.stopPropagation(); startCreate(s.name, "", "dir"); }}>
                          <FolderPlusIcon className="size-3 text-muted-foreground" />
                        </Button>
                        <Button size="icon" variant="ghost" className="size-5" title="스킬 삭제"
                          onClick={(e) => { e.stopPropagation(); setPendingDelete({ kind: "skill", skill: s.name }); }}>
                          <Trash2Icon className="size-3 text-destructive" />
                        </Button>
                      </span>
                    </div>

                    {/* 展开：递归文件树 */}
                    {isOpen && (
                      <>
                        {renderTree(tree, s.name, 0)}
                        {creating?.skill === s.name && creating.inDir === "" &&
                          inlineInputJSX(22)}
                      </>
                    )}
                  </div>
                );
              })}

              {skills.length === 0 && (
                <p className="p-3 text-xs text-muted-foreground">스킬이 없습니다. 「새로 만들기」로 시작하세요</p>
              )}
            </div>
          </ScrollArea>
        </div>

        {/* ── 右侧面板 ── */}
        <div className="flex flex-1 flex-col overflow-auto p-4">
          {!selected && (
            <SkillsOverview
              skills={skills}
              missing={missing}
              onSelect={(name) => setSelected({ skill: name, path: null })}
            />
          )}

          {selected && selected.path === null && selectedSkill && (
            <div className="max-w-5xl space-y-5">
              <div>
                <h2 className="font-sans text-base font-semibold">{selectedSkill.name}</h2>
                {selectedSkill.description && (
                  <p className="mt-1 text-sm text-muted-foreground">{selectedSkill.description}</p>
                )}
                <div className="mt-2 flex flex-wrap gap-2">
                  {selectedSkill.license && (
                    <Badge variant="outline" className="text-xs font-normal">License: {selectedSkill.license}</Badge>
                  )}
                  {selectedSkill.compatibility && (
                    <Badge variant="secondary" className="text-xs font-normal">{selectedSkill.compatibility}</Badge>
                  )}
                </div>
              </div>

              {/* 左右分栏：配置（MCP/可见性）在左为主，调用统计在右为辅。
                  lg 以下放不下时用 flex-row-reverse 回落到单列——统计因 DOM 顺序在前，
                  窄屏时自然落到配置上方（与改版前的上下顺序一致）。 */}
              <div className="flex flex-col gap-6 lg:flex-row-reverse lg:items-start">
                {/* ── 右侧：调用统计 ── */}
                <div className="space-y-2 lg:w-80 lg:shrink-0">
                  <Label className="text-xs text-muted-foreground">호출 통계</Label>
                  <div className="grid grid-cols-3 gap-2">
                    <div className="rounded-md border p-2">
                      <p className="text-lg font-semibold tabular-nums">{selectedSkill.calls}</p>
                      <p className="text-xs text-muted-foreground">총 호출 횟수</p>
                    </div>
                    <div className="rounded-md border p-2">
                      <p className="text-lg font-semibold tabular-nums">{selectedSkill.tasks}</p>
                      <p className="text-xs text-muted-foreground">사용한 작업 수</p>
                    </div>
                    <div className="rounded-md border p-2">
                      <p className="truncate text-sm font-medium" title={fmtTime(selectedSkill.last_used)}>
                        {fmtTime(selectedSkill.last_used)}
                      </p>
                      <p className="text-xs text-muted-foreground">최근 호출</p>
                    </div>
                  </div>
                  {selectedSkill.usage_agents.length > 0 && (
                    <div className="flex flex-wrap items-center gap-1">
                      <span className="text-xs text-muted-foreground">호출 에이전트:</span>
                      {selectedSkill.usage_agents.map((k) => (
                        <Badge key={k} variant="secondary" className="text-xs font-normal">{k}</Badge>
                      ))}
                    </div>
                  )}
                  {usageLoading ? (
                    <p className="text-xs text-muted-foreground">호출 상세 불러오는 중…</p>
                  ) : usageCalls.length > 0 ? (
                    <div className="rounded-md border">
                      <div className="border-b px-2 py-1 text-xs text-muted-foreground">최근 {usageCalls.length} 회 호출</div>
                      <div className="max-h-56 overflow-y-auto">
                        {usageCalls.map((c, i) => (
                          <div key={`${c.ts}-${i}`} className="flex items-center gap-2 border-b px-2 py-1 text-xs last:border-b-0">
                            <span className="tabular-nums text-muted-foreground">{fmtTime(c.ts)}</span>
                            <Badge variant="outline" className="font-normal">{c.agent_key || "—"}</Badge>
                            <span className="ml-auto text-muted-foreground">
                              {c.task_id > 0 ? `작업 #${c.task_id}` : c.session_id ? "대화 세션" : "—"}
                            </span>
                          </div>
                        ))}
                      </div>
                    </div>
                  ) : (
                    <p className="text-xs text-muted-foreground">호출 기록이 없습니다.</p>
                  )}
                </div>

                {/* ── 左侧：关联 MCP + 可见性 ── */}
                <div className="space-y-5 lg:min-w-0 lg:flex-1">
                  <div className="space-y-2">
                    <Label className="text-xs text-muted-foreground">
                      MCP 연결
                      <span className="ml-1 font-normal">(스킬을 불러올 때 해당 도구를 공개하고 사용 허용)</span>
                    </Label>
                    {mcpOptions.length === 0 ? (
                      <p className="text-xs text-muted-foreground">MCP가 없습니다. 「MCP」 페이지에서 추가하세요.</p>
                    ) : (
                      <div className="flex flex-wrap gap-x-4 gap-y-2">
                        {mcpOptions.map((m) => (
                          <label key={m.id} className="flex cursor-pointer items-center gap-2 text-sm">
                            <Checkbox
                              checked={detailMcps.includes(m.name)}
                              onCheckedChange={(on) => toggleSkillMcp(selectedSkill.name, m.name, !!on)}
                            />
                            {m.name}
                          </label>
                        ))}
                      </div>
                    )}
                  </div>

                  <div className="space-y-2">
                    <Label className="text-xs text-muted-foreground">접근 권한(에이전트별)</Label>
                    <div className="space-y-2">
                      {agents.map((a) => (
                        <label key={a.key} className="flex cursor-pointer items-center gap-2 text-sm">
                          <Checkbox
                            checked={(visibility[selectedSkill.name] ?? []).includes(a.id)}
                            onCheckedChange={() => toggleVisibility(selectedSkill.name, a.id, a.name)}
                          />
                          {a.name}
                        </label>
                      ))}
                      {agents.length === 0 && (
                        <span className="text-xs text-muted-foreground">(에이전트 없음)</span>
                      )}
                    </div>
                  </div>
                </div>
              </div>
            </div>
          )}

          {selected && selected.path !== null && (
            <div className="flex h-full flex-col gap-2">
              <div className="flex items-center gap-2 text-xs text-muted-foreground">
                <span className="font-medium text-foreground">{selected.skill}</span>
                <span>/</span>
                <span className="font-sans">{selected.path}</span>
                <Button size="sm" className="ml-auto" onClick={saveFile} disabled={!dirty || saving}>
                  {saving ? "저장 중…" : "저장"}
                </Button>
              </div>
              {fileLoading ? (
                <p className="text-xs text-muted-foreground">불러오는 중…</p>
              ) : (
                <Textarea
                  className="flex-1 resize-none font-sans text-xs"
                  value={fileContent}
                  onChange={(e) => { setFileContent(e.target.value); setDirty(true); }}
                />
              )}
            </div>
          )}
        </div>
      </div>

      {/* ── 删除二次确认 ── */}
      <AlertDialog open={!!pendingDelete} onOpenChange={(o) => { if (!o) setPendingDelete(null); }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {pendingDelete?.kind === "skill" && `삭제할 스킬: 「${pendingDelete.skill}」？`}
              {pendingDelete?.kind === "dir" && `삭제할 폴더: 「${pendingDelete.path}」？`}
              {pendingDelete?.kind === "file" && `삭제할 파일: 「${pendingDelete.path}」？`}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {pendingDelete?.kind === "skill"
                ? "이 스킬의 모든 파일, MCP 연결, 접근 권한 설정을 삭제합니다. 되돌릴 수 없습니다."
                : pendingDelete?.kind === "dir"
                  ? "폴더 안의 모든 파일도 삭제합니다. 되돌릴 수 없습니다."
                  : "이 작업은 되돌릴 수 없습니다."}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deleting}>취소</AlertDialogCancel>
            <AlertDialogAction
              disabled={deleting}
              onClick={(e) => { e.preventDefault(); void runPendingDelete(); }}
            >
              {deleting ? "삭제 중…" : "삭제"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {/* ── 新建 Skill 对话框 ── */}
      <Sheet open={newOpen} onOpenChange={setNewOpen}>
        <SheetContent side="right" className="flex w-full flex-col gap-0 data-[side=right]:sm:max-w-xl">
          <SheetHeader className="px-4 pt-4">
            <SheetTitle>새 스킬</SheetTitle>
          </SheetHeader>
          <Tabs defaultValue="basic" className="flex min-h-0 flex-1 flex-col">
            <TabsList className="mx-4 mt-3 shrink-0 justify-start">
              <TabsTrigger value="basic">기본 정보</TabsTrigger>
              <TabsTrigger value="mcp">
                MCP 연결
                {newMcps.length > 0 && (
                  <span className="ml-1.5 rounded-full bg-primary px-1.5 py-0.5 text-[10px] leading-none text-primary-foreground">
                    {newMcps.length}
                  </span>
                )}
              </TabsTrigger>
              <TabsTrigger value="visibility">
                접근 권한
                {newVisibility.length > 0 && (
                  <span className="ml-1.5 rounded-full bg-primary px-1.5 py-0.5 text-[10px] leading-none text-primary-foreground">
                    {newVisibility.length}
                  </span>
                )}
              </TabsTrigger>
            </TabsList>

            {/* 基本信息 */}
            <TabsContent value="basic" className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-4 pb-4 pt-4 data-[state=inactive]:hidden">
              <div className="grid gap-1.5">
                <Label htmlFor="sk-name">이름 <span className="text-destructive">*</span></Label>
                <Input id="sk-name" placeholder="sqli-deepdive" value={newName} onChange={(e) => setNewName(e.target.value)} />
                <p className="text-muted-foreground text-xs">영문 소문자 / 숫자 / 하이픈, 1~64자</p>
              </div>
              <div className="grid gap-1.5">
                <Label htmlFor="sk-desc">설명 <span className="text-destructive">*</span></Label>
                <Textarea id="sk-desc" rows={2} className="resize-none"
                  placeholder="이 스킬의 역할과 사용 시점을 설명하세요."
                  value={newDesc} onChange={(e) => setNewDesc(e.target.value)} />
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div className="grid gap-1.5">
                  <Label className="text-muted-foreground text-xs">license</Label>
                  <Input placeholder="MIT / Proprietary" value={newLicense} onChange={(e) => setNewLicense(e.target.value)} />
                </div>
                <div className="grid gap-1.5">
                  <Label className="text-muted-foreground text-xs">compatibility</Label>
                  <Input placeholder="필요 도구: sqlmap, python3" value={newCompat} onChange={(e) => setNewCompat(e.target.value)} />
                </div>
              </div>
              <div className="flex min-h-0 flex-1 flex-col gap-1.5">
                <Label htmlFor="sk-inst">본문 <span className="text-muted-foreground text-xs font-normal">(비워두면 기본 구조 자동 생성)</span></Label>
                <Textarea id="sk-inst"
                  className="min-h-40 flex-1 resize-none font-sans text-sm leading-relaxed"
                  placeholder={"## 실행 방법\n\n1. 오류를 먼저 확인합니다.\n2. 블라인드 인젝션 유형을 구분합니다.\n\n스크립트는 scripts/ 디렉터리에 저장합니다."}
                  value={newInst} onChange={(e) => setNewInst(e.target.value)} />
              </div>
            </TabsContent>

            {/* 关联 MCP */}
            <TabsContent value="mcp" className="overflow-y-auto px-4 pb-4 pt-4 data-[state=inactive]:hidden">
              <p className="mb-3 text-xs text-muted-foreground">스킬을 불러올 때 선택한 MCP 도구를 공개하고 사용을 허용합니다.</p>
              {mcpOptions.length === 0 ? (
                <p className="text-xs text-muted-foreground">MCP가 없습니다. 「MCP」 페이지에서 추가하세요.</p>
              ) : (
                <div className="flex flex-col gap-2">
                  {mcpOptions.map((m) => (
                    <label key={m.id} className="flex cursor-pointer items-center gap-2 text-sm">
                      <Checkbox
                        checked={newMcps.includes(m.name)}
                        onCheckedChange={(on) =>
                          setNewMcps((cur) => on ? [...cur, m.name] : cur.filter((n) => n !== m.name))
                        }
                      />
                      {m.name}
                    </label>
                  ))}
                </div>
              )}
            </TabsContent>

            {/* 可见性 */}
            <TabsContent value="visibility" className="overflow-y-auto px-4 pb-4 pt-4 data-[state=inactive]:hidden">
              <p className="mb-3 text-xs text-muted-foreground">생성 후 선택한 에이전트에서 이 스킬을 사용할 수 있습니다.</p>
              {agents.length === 0 ? (
                <p className="text-xs text-muted-foreground">(에이전트 없음)</p>
              ) : (
                <div className="flex flex-col gap-2">
                  {agents.map((a) => (
                    <label key={a.key} className="flex cursor-pointer items-center gap-2 text-sm">
                      <Checkbox
                        checked={newVisibility.includes(a.id)}
                        onCheckedChange={(on) =>
                          setNewVisibility((cur) => on ? [...cur, a.id] : cur.filter((id) => id !== a.id))
                        }
                      />
                      {a.name}
                    </label>
                  ))}
                </div>
              )}
            </TabsContent>
          </Tabs>

          <SheetFooter className="flex-row justify-end gap-2 border-t px-4 py-3">
            <Button variant="outline" onClick={() => setNewOpen(false)}>취소</Button>
            <Button onClick={createNewSkill} disabled={creatingSkill}>{creatingSkill ? "생성 중…" : "생성"}</Button>
          </SheetFooter>
        </SheetContent>
      </Sheet>
    </div>
  );
}
