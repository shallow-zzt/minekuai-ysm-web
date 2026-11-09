"use client";

import { useCallback, useEffect, useRef, useState } from "react";

type Access = { upload_required: boolean; reload_required: boolean };
type Model = { name: string; size: number; modified: string };
type Library = { files: Model[]; total_bytes: number; max_upload_bytes: number; max_library_bytes: number; heartbeat: { time: string } | null };
type Job = { id: string; status: string; message: string; file?: string };
const sizeLabel = (n: number) => n < 1048576 ? `${Math.round(n / 1024)} KB` : `${(n / 1048576).toFixed(1)} MB`;
const cleanName = (n: string) => n.replace(/^[a-f0-9]{32}_/, "");

function Icon({ kind, size = 20 }: { kind: string; size?: number }) {
  const paths: Record<string, React.ReactNode> = {
    box: <><path d="m12 3 9 5-9 5-9-5 9-5Z M3 8v9l9 5 9-5V8 M12 13v9 M7.5 5.5l9 5" /></>,
    upload: <><path d="M12 16V3m-5 5 5-5 5 5M4 15v5h16v-5" /></>,
    arrow: <><path d="M4 12h16m-6-6 6 6-6 6" /></>,
    refresh: <><path d="M20 7v5h-5M4 17v-5h5 M6 6a8 8 0 0 1 13 3M5 15a8 8 0 0 0 13 3" /></>,
    search: <><circle cx="10" cy="10" r="6" /><path d="m15 15 5 5" /></>,
    check: <path d="m5 12 4 4L19 6" />,
    key: <><circle cx="8" cy="8" r="5" /><path d="m12 12 9 9m-3-3 3-3m-6 0 3-3" /></>,
    folder: <path d="M3 7a2 2 0 0 1 2-2h5l2 2h7a2 2 0 0 1 2 2v10H3V7Z" />,
    home: <><path d="m3 10 9-7 9 7M5 9v12h14V9M9 21v-8h6v8" /></>,
    chevron: <path d="m9 5 7 7-7 7" />,
    close: <path d="m6 6 12 12M6 18 18 6" />,
    plus: <path d="M12 4v16M4 12h16" />,
    info: <><circle cx="12" cy="12" r="9" /><path d="M12 11v6M12 7h.01" /></>,
    sparkle: <path d="m12 2 2.5 7.5L22 12l-7.5 2.5L12 22l-2.5-7.5L2 12l7.5-2.5L12 2Z" />,
    sort: <path d="M8 4v16m-4-4 4 4 4-4M16 20V4m-4 4 4-4 4 4" />,
    archive: <><rect x="4" y="3" width="16" height="18" rx="3" /><path d="M10 3v3h4v3h-4v3h4v3h-4v3h4" /></>,
  };
  return <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">{paths[kind] || paths.box}</svg>;
}

export default function Home() {
  const [access, setAccess] = useState<Access | null>(null);
  const [token, setToken] = useState("");
  const [draftToken, setDraftToken] = useState("");
  const [library, setLibrary] = useState<Library | null>(null);
  const [file, setFile] = useState<File | null>(null);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [progress, setProgress] = useState(0);
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(false);
  const [drag, setDrag] = useState(false);
  const [search, setSearch] = useState("");
  const [fileType, setFileType] = useState("all");
  const [sort, setSort] = useState<"name" | "size" | "modified">("modified");
  const [ascending, setAscending] = useState(false);
  const [job, setJob] = useState<Job | null>(null);
  const [adminOpen, setAdminOpen] = useState(false);
  const [adminToken, setAdminToken] = useState("");
  const [reloadBusy, setReloadBusy] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  const uploadDialog = useRef<HTMLDialogElement>(null);
  const searchInput = useRef<HTMLInputElement>(null);
  const ready = access !== null && (!access.upload_required || token !== "");

  const api = useCallback(async (url: string, init?: RequestInit, key = token) => {
    const response = await fetch(url, { ...init, headers: { ...(key ? { Authorization: `Bearer ${key}` } : {}), ...init?.headers } });
    const body = await response.json();
    if (!response.ok) throw new Error(body.error || "请求失败，请稍后重试");
    return body;
  }, [token]);

  const refresh = useCallback(async () => {
    setLoading(true);
    try { setLibrary(await api("/api/models")); setError(""); }
    catch (e) { setError((e as Error).message); }
    finally { setLoading(false); }
  }, [api]);

  useEffect(() => { const saved = sessionStorage.getItem("ysm-token"); if (saved) setToken(saved); }, []);
  useEffect(() => {
    const controller = new AbortController();
    fetch("/api/access", { signal: controller.signal }).then(async response => {
      if (!response.ok) throw new Error("读取站点设置失败，请重新连接");
      setAccess(await response.json());
    }).catch(e => { if (!controller.signal.aborted) setError((e as Error).message); });
    return () => controller.abort();
  }, []);
  useEffect(() => { if (ready) void refresh(); }, [ready, refresh]);
  useEffect(() => {
    if (!ready || !job || !["pending", "unknown"].includes(job.status)) return;
    let active = true;
    let attempts = 0;
    let timer: ReturnType<typeof setTimeout>;
    const poll = async () => {
      try {
        const updated: Job = await api(`/api/jobs/${job.id}`);
        if (!active) return;
        setJob(updated);
        if (!["pending", "unknown"].includes(updated.status)) { void refresh(); return; }
      } catch (e) { if (active) setError((e as Error).message); }
      if (active && ++attempts < 60) timer = setTimeout(poll, 5000);
      else if (active) setNotice("已等待 5 分钟。服务器上线后仍可处理请求，可点击检查回执再次查看。");
    };
    timer = setTimeout(poll, 5000);
    return () => { active = false; clearTimeout(timer); };
  }, [job?.id, ready, api, refresh]); // One bounded polling loop per request.

  function choose(selected: File | undefined) {
    if (!selected) return;
    setNotice(""); setError(""); setFile(null);
    if (!/\.(ysm|zip)$/i.test(selected.name)) { setError("请选择 .ysm 或 .zip 模型包"); return; }
    if (selected.size > (library?.max_upload_bytes || 64 * 1048576)) { setError("这个文件超出了上传大小限制"); return; }
    setFile(selected); setProgress(0);
  }

  async function upload() {
    if (!file || !ready || busy) return;
    setBusy(true); setError(""); setNotice(""); setProgress(0);
    const form = new FormData(); form.append("file", file);
    try {
      const result = await new Promise<Job>((resolve, reject) => {
        const xhr = new XMLHttpRequest();
        xhr.open("POST", "/api/models"); xhr.timeout = 240000;
        if (token) xhr.setRequestHeader("Authorization", `Bearer ${token}`);
        xhr.upload.onprogress = e => { if (e.lengthComputable) setProgress(Math.round(e.loaded / e.total * 100)); };
        xhr.onerror = () => reject(new Error("连接中断，请刷新模型列表核实上传结果"));
        xhr.ontimeout = () => reject(new Error("上传超时，请刷新模型列表核实上传结果"));
        xhr.onload = () => {
          try { const body = JSON.parse(xhr.responseText); if (xhr.status >= 200 && xhr.status < 300) resolve(body); else reject(new Error(body.error || "上传失败")); }
          catch { reject(new Error("服务器返回异常，请稍后重试")); }
        };
        xhr.send(form);
      });
      setJob(result); setFile(null); if (input.current) input.current.value = "";
      uploadDialog.current?.close();
      await refresh();
    } catch (e) { setError((e as Error).message); }
    finally { setBusy(false); }
  }

  async function reload() {
    setReloadBusy(true); setError("");
    try { setJob(await api("/api/reload", { method: "POST" }, adminToken || token)); setAdminToken(""); setAdminOpen(false); }
    catch (e) { setError((e as Error).message); }
    finally { setReloadBusy(false); }
  }


  function openUpload() {
    if (!uploadDialog.current?.open) uploadDialog.current?.showModal();
  }

  function changeSort(column: "name" | "size" | "modified") {
    setAscending(sort === column ? !ascending : column === "name");
    setSort(column);
  }

  const heartbeatAge = library?.heartbeat ? Date.now() - Date.parse(library.heartbeat.time) : Infinity;
  const online = Number.isFinite(heartbeatAge) && heartbeatAge < 30000;
  const allModels = library?.files || [];
  const models = allModels.filter(model =>
    cleanName(model.name).toLowerCase().includes(search.toLowerCase()) &&
    (fileType === "all" || model.name.toLowerCase().endsWith(`.${fileType}`))
  ).sort((a, b) => {
    const comparison = sort === "name" ? cleanName(a.name).localeCompare(cleanName(b.name), "zh-CN") :
      sort === "size" ? a.size - b.size : Date.parse(a.modified) - Date.parse(b.modified);
    return ascending ? comparison : -comparison;
  });
  const capacity = library ? Math.min(100, library.total_bytes / library.max_library_bytes * 100) : 0;
  const uploadLimit = sizeLabel(library?.max_upload_bytes || 64 * 1048576);
  const sortDirection = (column: string) => sort === column ? ascending ? "ascending" as const : "descending" as const : "none" as const;

  return <div className="site-shell">
    <div className="background-stars" aria-hidden="true"><i /><i /><i /><i /></div>
    <header className="site-header">
      <a className="brand" href="/" aria-label="衣橱首页">
        <span className="brand-mark"><Icon kind="folder" size={26} /><i /></span>
        <span className="brand-name">衣橱 <span>YSM WARDROBE</span></span>
      </a>
      <div className="header-actions">
        <span className="edition">Minecraft 1.21.1</span>
        <a className="header-help" href="https://yesstevemodel.github.io/en/wiki/faq/" target="_blank" rel="noreferrer"><Icon kind="info" size={17} /><span>使用帮助</span></a>
      </div>
    </header>

    <main className="main-content">
      <section className="page-intro">
        <div>
          <div className="eyebrow"><span /> A LITTLE SPACE FOR YOUR MODELS</div>
          <h1>服务器衣橱<Icon kind="sparkle" size={24} /></h1>
          <p>收藏喜欢的模样，在下一场冒险里见。</p>
        </div>
        <div className="intro-decoration" aria-hidden="true"><div className="folder-back" /><div className="folder-front"><Icon kind="sparkle" size={32} /></div><span className="decoration-star">✦</span><span className="decoration-dot" /></div>
      </section>

      {access === null ? <section className="connection-panel surface"><Icon kind="refresh" size={28} /><h2>正在连接衣橱</h2><p>正在读取站点设置…</p><button className="secondary" onClick={() => location.reload()}>重新连接</button></section> : !ready ?
        <section className="connection-panel surface">
          <span className="connection-icon"><Icon kind="key" size={28} /></span>
          <h2>打开服务器衣橱</h2><p>输入服主提供的上传口令，即可查看和上传模型。</p>
          <form onSubmit={event => { event.preventDefault(); sessionStorage.setItem("ysm-token", draftToken); setToken(draftToken); setDraftToken(""); }}>
            <label className="sr-only" htmlFor="token">上传口令</label>
            <input id="token" type="password" value={draftToken} onChange={event => setDraftToken(event.target.value)} placeholder="输入上传口令" required autoComplete="off" />
            <button className="primary" type="submit">进入衣橱<Icon kind="arrow" size={18} /></button>
          </form>
        </section> : <>
          <div className="directory-bar">
            <nav className="breadcrumbs" aria-label="模型目录"><button aria-label="显示全部模型" onClick={() => { setSearch(""); setFileType("all"); }}><Icon kind="home" size={17} /></button><Icon kind="chevron" size={13} /><span>服务器</span><Icon kind="chevron" size={13} /><strong>模型衣橱</strong></nav>
            <span className={`bridge-status ${online ? "online" : ""}`}><i />{online ? "重载桥在线" : "等待重载桥连接"}</span>
          </div>

          <section className="file-browser surface" aria-label="服务器模型文件">
            <div className="browser-toolbar">
              <div className="browser-title"><span className="small-folder"><Icon kind="folder" size={22} /></span><h2>全部模型</h2><span className="count">{library ? allModels.length : "—"}</span></div>
              <div className="toolbar-actions">
                <button className={`icon-button ${loading ? "is-loading" : ""}`} aria-label="刷新模型列表" title="刷新模型列表" onClick={refresh} disabled={loading}><Icon kind="refresh" size={19} /></button>
                <button className="secondary reload-trigger" aria-label={access.reload_required ? "管理员重载" : "申请重载"} onClick={() => setAdminOpen(!adminOpen)} aria-expanded={adminOpen}><Icon kind="refresh" size={16} /><span>{access.reload_required ? "管理员重载" : "申请重载"}</span></button>
                <button className="primary" onClick={openUpload}><Icon kind="plus" size={18} />上传模型</button>
              </div>
            </div>

            <div className="filter-bar">
              <div className="file-filters" aria-label="文件类型筛选">
                {[{value:"all",label:"全部文件"},{value:"ysm",label:"YSM 模型"},{value:"zip",label:"ZIP 压缩包"}].map(type => <button key={type.value} aria-pressed={fileType === type.value} onClick={() => setFileType(type.value)}>{type.label}</button>)}
              </div>
              <div className="search-field"><Icon kind="search" size={18} /><input ref={searchInput} aria-label="搜索模型" placeholder="搜索模型名称…" value={search} onChange={event => setSearch(event.target.value)} />{search && <button aria-label="清空搜索" onClick={() => { setSearch(""); searchInput.current?.focus(); }}><Icon kind="close" size={14} /></button>}</div>
            </div>

            <div className="file-table-wrap" aria-busy={loading}>
              <table className="file-table">
                <thead><tr>
                  <th aria-sort={sortDirection("name")}><button onClick={() => changeSort("name")}>文件名称<Icon kind="sort" size={13} /></button></th>
                  <th className="type-column">类型</th>
                  <th className="size-column" aria-sort={sortDirection("size")}><button onClick={() => changeSort("size")}>大小<Icon kind="sort" size={13} /></button></th>
                  <th className="date-column" aria-sort={sortDirection("modified")}><button onClick={() => changeSort("modified")}>修改时间<Icon kind="sort" size={13} /></button></th>
                </tr></thead>
                <tbody>{models.map(model => {
                  const zip = model.name.toLowerCase().endsWith(".zip");
                  return <tr key={model.name}>
                    <td><div className="file-cell"><span className={`file-icon ${zip ? "zip" : ""}`}><Icon kind={zip ? "archive" : "box"} size={24} /></span><div className="file-name"><span title={model.name}>{cleanName(model.name)}</span><small>{zip ? "模型压缩包" : "Yes Steve Model"}</small></div></div></td>
                    <td className="type-column"><span className={`file-type ${zip ? "zip" : ""}`}>{zip ? "ZIP" : "YSM"}</span></td>
                    <td className="size-column">{sizeLabel(model.size)}</td>
                    <td className="date-column"><time dateTime={model.modified}>{new Date(model.modified).toLocaleDateString("zh-CN", { year:"numeric", month:"2-digit", day:"2-digit" })}<span>{new Date(model.modified).toLocaleTimeString("zh-CN", { hour:"2-digit", minute:"2-digit", hour12:false })}</span></time></td>
                  </tr>;
                })}</tbody>
              </table>
              {!models.length && <div className="empty-state"><span><Icon kind={search ? "search" : "folder"} size={42} /></span><h3>{loading ? "正在整理衣橱…" : search || fileType !== "all" ? "没有找到匹配的模型" : library ? "衣橱等待你的第一件作品" : "等待连接服务器"}</h3><p>{search || fileType !== "all" ? "试试其他关键词或文件类型" : "上传一个模型，让新的模样在这里安家。"}</p></div>}
            </div>

            <div className="browser-footer"><span>{library ? `共 ${allModels.length} 个文件${models.length !== allModels.length ? ` · 当前显示 ${models.length} 个` : ""}` : "正在连接服务器"}</span><div className="capacity"><span>{library ? `${sizeLabel(library.total_bytes)} / ${sizeLabel(library.max_library_bytes)}` : "容量待获取"}</span><meter min="0" max="100" value={capacity} aria-label="模型库容量使用率" /></div></div>
          </section>

          <button className={`quick-upload ${drag ? "dragging" : ""}`} onClick={openUpload} onDragOver={event => { event.preventDefault(); if (!busy) setDrag(true); }} onDragLeave={() => setDrag(false)} onDrop={event => { event.preventDefault(); setDrag(false); if (!busy) { openUpload(); choose(event.dataTransfer.files[0]); } }}>
            <span className="quick-upload-icon"><Icon kind="upload" size={21} /></span><span><strong>拖入模型，或点击上传</strong><small>支持 .ysm / .zip，单文件最多 {uploadLimit}</small></span><Icon kind="plus" size={21} />
          </button>
        </>}

      <div className="notifications" aria-live="polite">
        {error && <div className="message error"><Icon kind="info" size={19} /><span>{error}</span></div>}
        {notice && <div className="message"><Icon kind="info" size={19} /><span>{notice}</span></div>}
        {job && <div className={`message job ${job.status === "failed" ? "error" : ""}`}><span className="job-icon"><Icon kind={job.status === "dispatched" ? "check" : "refresh"} /></span><div><strong>{job.status === "dispatched" ? "重载命令已执行" : job.status === "pending" ? "模型正在等待重载" : "上传状态"}</strong><p>{job.message}</p><small>请求 {job.id}</small></div><button className="text-button" onClick={async () => { try { setJob(await api(`/api/jobs/${job.id}`)); } catch (e) { setError((e as Error).message); } }}>检查回执<Icon kind="chevron" size={13} /></button></div>}
      </div>

      {adminOpen && <section className="reload-panel surface"><div><h3>申请服务器重载</h3><p>手动添加模型或重试时，发送一次重载请求。</p></div><form onSubmit={event => { event.preventDefault(); void reload(); }}>{access?.reload_required && <input aria-label="管理员口令" type="password" value={adminToken} onChange={event => setAdminToken(event.target.value)} placeholder="输入管理员口令" required autoComplete="off" />}<button className="primary" disabled={reloadBusy}>{reloadBusy ? "正在提交…" : "提交重载"}</button><button className="icon-button" type="button" aria-label="关闭重载面板" onClick={() => setAdminOpen(false)}><Icon kind="close" size={18} /></button></form></section>}

      <section className="guide" aria-label="使用流程"><div><span>01</span><strong>上传模型</strong><p>选择完整的 YSM 模型包</p></div><i /><div><span>02</span><strong>等待重载</strong><p>通常在 5–35 秒内执行</p></div><i /><div><span>03</span><strong>游戏中见</strong><p>等待同步后，选择你的新模型</p></div></section>
      <p className="usage-note"><Icon kind="info" size={14} />模型将向全服玩家开放。YSM 加载与同步需要一些时间，可在游戏中确认结果。</p>
    </main>

    <footer className="site-footer"><span><Icon kind="sparkle" size={14} />每一种模样，都值得被收藏。</span><div><span>衣橱 · Yes Steve Model</span>{access?.upload_required && token && <button onClick={() => { sessionStorage.removeItem("ysm-token"); setToken(""); setLibrary(null); setJob(null); setError(""); setAdminOpen(false); }}>退出衣橱</button>}</div></footer>

    <dialog className="upload-dialog surface" ref={uploadDialog} aria-labelledby="upload-title" onCancel={event => { if (busy) event.preventDefault(); }}>
      <div className="dialog-heading"><span className="dialog-icon"><Icon kind="upload" size={23} /></span><div><h2 id="upload-title">添加一份新模样</h2><p>上传到服务器衣橱</p></div><button className="icon-button" aria-label="关闭上传窗口" disabled={busy} onClick={() => uploadDialog.current?.close()}><Icon kind="close" size={20} /></button></div>
      <button className={`dropzone ${drag ? "dragging" : ""}`} disabled={busy} onClick={() => input.current?.click()} onDragOver={event => { event.preventDefault(); if (!busy) setDrag(true); }} onDragLeave={() => setDrag(false)} onDrop={event => { event.preventDefault(); setDrag(false); if (!busy) choose(event.dataTransfer.files[0]); }}>
        <span className="dropzone-icon"><Icon kind={file ? "box" : "upload"} size={32} /></span><strong>{file ? file.name : "把模型拖到这里"}</strong><span>{file ? sizeLabel(file.size) : "或点击选择本地文件"}</span><small>.ysm / .zip · 单文件最多 {uploadLimit}</small>
      </button>
      <input ref={input} type="file" accept=".ysm,.zip" className="sr-only" tabIndex={-1} onChange={event => choose(event.target.files?.[0])} disabled={busy} />
      {error && <p className="dialog-error" role="alert">{error}</p>}
      {busy && <div className="upload-progress"><progress value={progress} max="100" aria-label="上传进度" /><span>{progress < 100 ? `已上传 ${progress}%` : "正在传送到服务器…"}</span></div>}
      <p className="dialog-note">上传你有权使用的模型。文件传送完成后，会自动申请服务器重载。</p>
      <div className="dialog-actions"><button className="secondary" disabled={busy} onClick={() => uploadDialog.current?.close()}>取消</button><button className="primary" disabled={!file || busy || !library} onClick={upload}>{busy ? "正在上传…" : "上传到服务器"}<Icon kind="arrow" size={18} /></button></div>
    </dialog>
  </div>;
}
