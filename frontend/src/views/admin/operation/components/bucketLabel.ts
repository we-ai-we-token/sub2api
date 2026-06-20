/**
 * 把桶起点 ISO 时间戳格式化为本地时间(管理员浏览器时区)的紧凑标签。
 * 1h 粒度: "MM-DD HHh"（如 06-21 02h）
 * 5m 粒度: "MM-DD HH:mm"（如 06-21 02:05）
 */
export function formatBucketLabel(iso: string, bucket: '5m' | '1h'): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const mm = String(d.getMonth() + 1).padStart(2, '0')
  const dd = String(d.getDate()).padStart(2, '0')
  const hh = String(d.getHours()).padStart(2, '0')
  if (bucket === '5m') {
    const mi = String(d.getMinutes()).padStart(2, '0')
    return `${mm}-${dd} ${hh}:${mi}`
  }
  return `${mm}-${dd} ${hh}h`
}
