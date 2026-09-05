import { useEffect, useState } from 'react'
import { invoke } from '@tauri-apps/api/core'

interface Release {
  currentVersion: string
  version: string
  available: boolean
  repository: string
  notes: string
  assetName: string | null
}

// Share in-flight checks, including React StrictMode's development remount.
let pendingCheck: Promise<Release> | undefined
function requestRelease() {
  pendingCheck ??= invoke<Release>('check_desktop_update').finally(() => { pendingCheck = undefined })
  return pendingCheck
}
function releaseMessage(latest: Release) {
  return latest.available ? (latest.assetName ? '发现新版本，可下载完整便携包。' : '新版本尚未提供当前架构的 ZIP。') : '当前已是最新版本。'
}

export function DesktopUpdate() {
  const [open, setOpen] = useState(false)
  const [release, setRelease] = useState<Release>()
  const [busy, setBusy] = useState<'checking' | 'installing'>()
  const [message, setMessage] = useState('')
  const [previousResult, setPreviousResult] = useState('')
  useEffect(() => {
    if (!window.__MATCH_DESKTOP__) return
    let disposed = false
    let statusTimer: ReturnType<typeof setTimeout> | undefined
    const statusDeadline = Date.now() + 60_000
    setBusy('checking')
    void requestRelease()
      .then((latest) => { if (!disposed) { setRelease(latest); setMessage(releaseMessage(latest)) } })
      .catch((error: unknown) => { if (!disposed) setMessage(`自动检查失败，可手动重试：${String(error)}`) })
      .finally(() => { if (!disposed) setBusy(undefined) })
    function readStatus() {
      void invoke<{ ok: boolean | null; pending?: boolean; message: string } | null>('desktop_update_status')
        .then((status) => {
          if (disposed || !status) return
          setPreviousResult(status.message)
          if (status.pending && Date.now() < statusDeadline) statusTimer = setTimeout(readStatus, 500)
        })
        .catch((error: unknown) => { if (!disposed) setPreviousResult(String(error)) })
    }
    readStatus()
    return () => { disposed = true; clearTimeout(statusTimer) }
  }, [])
  if (!window.__MATCH_DESKTOP__) return null

  async function check() {
    setBusy('checking'); setMessage('正在检查 GitHub Release…'); setRelease(undefined)
    try {
      const latest = await requestRelease()
      setRelease(latest)
      setMessage(releaseMessage(latest))
    } catch (error) { setMessage(`检查失败：${String(error)}`) }
    finally { setBusy(undefined) }
  }
  async function install() {
    if (!release) return
    setBusy('installing'); setMessage('正在下载并校验完整 ZIP，完成后自动退出并重启。请勿关闭客户端。')
    try { await invoke('install_desktop_update', { version: release.version }) }
    catch (error) { setMessage(`更新失败：${String(error)}`); setBusy(undefined) }
  }
  return <>
    <button type="button" onClick={() => setOpen(true)}>客户端更新{release?.available ? ` · 新版 ${release.version}` : ''}</button>
    {open && <div className="desktop-update-backdrop">
      <section role="dialog" aria-modal="true" aria-labelledby="desktop-update-title" className="desktop-update-dialog">
        <h2 id="desktop-update-title">客户端更新</h2>
        {release && <p>当前 {release.currentVersion} → 最新 {release.version}<br />来源：{release.repository}</p>}
        <p role="status" aria-live="polite">{message || '检查当前仓库的最新稳定版。'}</p>
        {previousResult && <p>上次更新结果：{previousResult}</p>}
        {release?.notes && <details><summary>版本说明</summary><pre>{release.notes}</pre></details>}
        <p>更新将重启模拟器，当前内存中的 Tickets 和比赛历史会清空。请先保存未提交的规则编辑。旧程序目录会保留为备份；新版本启动失败时自动恢复。</p>
        <div className="desktop-update-actions">
          <button type="button" disabled={!!busy} onClick={() => void check()}>{busy === 'checking' ? '检查中…' : '检查更新'}</button>
          {release?.available && release.assetName && <button type="button" disabled={!!busy} onClick={() => void install()}>{busy === 'installing' ? '更新中…' : '下载更新并重启'}</button>}
          <button type="button" disabled={busy === 'installing'} onClick={() => setOpen(false)}>关闭</button>
        </div>
      </section>
    </div>}
  </>
}
