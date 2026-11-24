import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { saveJsonFile } from './saveJsonFile'

const native = vi.hoisted(() => ({ invoke: vi.fn() }))
vi.mock('@tauri-apps/api/core', () => ({ invoke: native.invoke }))

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((success, failure) => {
    resolve = success
    reject = failure
  })
  return { promise, resolve, reject }
}

beforeEach(() => native.invoke.mockReset())
afterEach(() => vi.unstubAllGlobals())

describe('选择位置并保存 JSON', () => {
  it('点击时立即打开浏览器选择器，选择后读取原文，关闭写入流后才报告成功', async () => {
    const close = deferred<void>()
    const stream = {
      write: vi.fn().mockResolvedValue(undefined),
      close: vi.fn(() => close.promise),
      abort: vi.fn().mockResolvedValue(undefined),
    }
    const handle = { name: '用户选择.json', createWritable: vi.fn().mockResolvedValue(stream) }
    const picker = vi.fn().mockResolvedValue(handle)
    const raw = '{"large":9223372036854775807,"note":"原始中文"}'
    const getText = vi.fn().mockResolvedValue(raw)
    vi.stubGlobal('window', { showSaveFilePicker: picker })

    const result = saveJsonFile('suggested.json', getText)
    expect(picker).toHaveBeenCalledOnce()
    expect(picker).toHaveBeenCalledWith({
      suggestedName: 'suggested.json',
      types: [{ description: 'JSON 文件', accept: { 'application/json': ['.json'] } }],
    })
    expect(getText).not.toHaveBeenCalled()
    await vi.waitFor(() => expect(stream.close).toHaveBeenCalledOnce())
    expect(stream.write).toHaveBeenCalledWith(raw)
    let finished = false
    void result.then(() => (finished = true))
    await Promise.resolve()
    expect(finished).toBe(false)
    close.resolve()
    expect(await result).toBe('用户选择.json')
    expect(stream.abort).not.toHaveBeenCalled()
  })

  it('取消选择时不请求内容，也不写入文件', async () => {
    const picker = vi.fn().mockRejectedValue(new DOMException('cancelled', 'AbortError'))
    const getText = vi.fn()
    vi.stubGlobal('window', { showSaveFilePicker: picker })
    await expect(saveJsonFile('x.json', getText)).resolves.toBeNull()
    expect(getText).not.toHaveBeenCalled()
  })

  it('内容读取失败时不打开写入流', async () => {
    const handle = { name: 'x.json', createWritable: vi.fn() }
    vi.stubGlobal('window', { showSaveFilePicker: vi.fn().mockResolvedValue(handle) })
    await expect(
      saveJsonFile('x.json', () => Promise.reject(new Error('读取失败'))),
    ).rejects.toThrow('读取失败')
    expect(handle.createWritable).not.toHaveBeenCalled()
  })

  it('写入流的 AbortError 仍是写入失败，且回滚尚未关闭的流', async () => {
    const error = new DOMException('写入中断', 'AbortError')
    const stream = {
      write: vi.fn().mockRejectedValue(error),
      close: vi.fn(),
      abort: vi.fn().mockResolvedValue(undefined),
    }
    const handle = { name: 'x.json', createWritable: vi.fn().mockResolvedValue(stream) }
    vi.stubGlobal('window', { showSaveFilePicker: vi.fn().mockResolvedValue(handle) })
    await expect(saveJsonFile('x.json', () => '{}')).rejects.toBe(error)
    expect(stream.abort).toHaveBeenCalledOnce()
    expect(stream.close).not.toHaveBeenCalled()
  })

  it('关闭写入流失败时不报告成功', async () => {
    const stream = {
      write: vi.fn().mockResolvedValue(undefined),
      close: vi.fn().mockRejectedValue(new Error('磁盘已满')),
      abort: vi.fn().mockResolvedValue(undefined),
    }
    const handle = { name: 'x.json', createWritable: vi.fn().mockResolvedValue(stream) }
    vi.stubGlobal('window', { showSaveFilePicker: vi.fn().mockResolvedValue(handle) })
    await expect(saveJsonFile('x.json', () => '{}')).rejects.toThrow('磁盘已满')
    expect(stream.abort).toHaveBeenCalledOnce()
  })

  it('浏览器不支持选择器时明确提示，不发起默认目录下载或内容请求', async () => {
    const getText = vi.fn()
    vi.stubGlobal('window', {})
    await expect(saveJsonFile('x.json', getText)).rejects.toThrow('兼容浏览器或桌面客户端')
    expect(getText).not.toHaveBeenCalled()
  })

  it('桌面先取得内容，再通过原生命令保存并返回实际路径', async () => {
    const content = deferred<string>()
    const getText = vi.fn(() => content.promise)
    vi.stubGlobal('window', { __MATCH_DESKTOP__: true, showSaveFilePicker: vi.fn() })
    native.invoke.mockResolvedValue('C:\\用户\\场景.json')
    const result = saveJsonFile('scene.json', getText)
    expect(getText).toHaveBeenCalledOnce()
    expect(native.invoke).not.toHaveBeenCalled()
    content.resolve('{"large":9223372036854775807}')
    expect(await result).toBe('C:\\用户\\场景.json')
    expect(native.invoke).toHaveBeenCalledWith('save_json_export', {
      fileName: 'scene.json',
      content: '{"large":9223372036854775807}',
    })
  })

  it('桌面取消返回空值，字符串类型的原生错误保留详细信息', async () => {
    vi.stubGlobal('window', { __MATCH_DESKTOP__: true })
    native.invoke.mockResolvedValueOnce(null).mockRejectedValueOnce('无权写入该目录')
    await expect(saveJsonFile('x.json', () => '{}')).resolves.toBeNull()
    await expect(saveJsonFile('x.json', () => '{}')).rejects.toThrow('无权写入该目录')
  })

  it('桌面读取内容失败时不显示原生选择器', async () => {
    vi.stubGlobal('window', { __MATCH_DESKTOP__: true })
    await expect(
      saveJsonFile('x.json', () => Promise.reject(new Error('接口失败'))),
    ).rejects.toThrow('接口失败')
    expect(native.invoke).not.toHaveBeenCalled()
  })
})
