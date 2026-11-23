/** 根据物理节点标识构造模拟器进程内端点。 */
export function inProcessEndpoint(id: string): string {
  return `inproc://${id}`
}
