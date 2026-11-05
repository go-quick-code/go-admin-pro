// Excel 模板/导出的浏览器下载（issue #20）。
// 后端返回 xlsx 二进制并带 Content-Disposition；这里解析文件名并触发下载，
// 拿不到文件名时用调用方给的兜底名（统一补 .xlsx 后缀）。

// saveBlobResponse 把 axios（responseType: 'blob'）的成功响应保存为文件。
export function saveBlobResponse(response: any, fallbackName: string) {
  saveBlobAs(response, ensureXlsxSuffix(fallbackName))
}

// saveBlobAs 把二进制响应按后端 Content-Disposition 的文件名保存；
// 拿不到文件名时用 fallbackName 原样（附件等非 Excel 文件使用，不补 .xlsx）。
export function saveBlobAs(response: any, fallbackName: string) {
  const data = response?.data
  const blob = data instanceof Blob ? data : new Blob([data ?? ''])
  const filename =
    parseDispositionFilename(response?.headers?.['content-disposition']) || fallbackName
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = filename
  anchor.style.display = 'none'
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
  URL.revokeObjectURL(url)
}

// parseDispositionFilename 解析 attachment 头：优先 RFC 5987 的 filename*，
// 其次 ASCII 的 filename。无法解析返回 undefined。
export function parseDispositionFilename(disposition?: string): string | undefined {
  if (!disposition) return undefined
  const encoded = /filename\*=UTF-8''([^;]+)/i.exec(disposition)
  if (encoded) {
    try {
      return decodeURIComponent(encoded[1])
    } catch {
      return encoded[1]
    }
  }
  const plain = /filename="?([^";]+)"?/i.exec(disposition)
  return plain ? plain[1] : undefined
}

// ensureXlsxSuffix 保证浏览器下载名带 .xlsx。
export function ensureXlsxSuffix(name: string): string {
  return /\.xlsx$/i.test(name) ? name : `${name}.xlsx`
}
