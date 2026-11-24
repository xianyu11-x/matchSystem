import { invoke } from '@tauri-apps/api/core'

type SavePickerWindow = Window & {
  showSaveFilePicker?: (options: {
    suggestedName: string
    types: Array<{ description: string; accept: Record<string, string[]> }>
  }) => Promise<FileSystemFileHandle>
}

export async function saveJsonFile(
  fileName: string,
  getText: () => string | Promise<string>,
): Promise<string | null> {
  if (window.__MATCH_DESKTOP__) {
    const content = await getText()
    try {
      return await invoke<string | null>('save_json_export', { fileName, content })
    } catch (error) {
      throw typeof error === 'string' ? new Error(error) : error
    }
  }

  const picker = (window as SavePickerWindow).showSaveFilePicker
  if (!picker) throw new Error('当前浏览器不支持选择保存位置，请使用兼容浏览器或桌面客户端。')

  let handle: FileSystemFileHandle
  try {
    handle = await picker.call(window, {
      suggestedName: fileName,
      types: [{ description: 'JSON 文件', accept: { 'application/json': ['.json'] } }],
    })
  } catch (error) {
    if (error && typeof error === 'object' && 'name' in error && error.name === 'AbortError')
      return null
    throw error
  }

  const content = await getText()
  const stream = await handle.createWritable()
  try {
    await stream.write(content)
    await stream.close()
  } catch (error) {
    await stream.abort().catch(() => undefined)
    throw error
  }
  return handle.name
}
