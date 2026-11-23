/**
 * 把 source 的文本写入剪贴板，返回是否成功。
 * 非安全上下文或权限被拒时剪贴板不可写，改为选中 source，方便用户手动复制。
 */
export async function copyText(source: HTMLElement): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(source.textContent ?? "");
    return true;
  } catch {
    const range = document.createRange();
    range.selectNodeContents(source);
    const selection = window.getSelection();
    selection?.removeAllRanges();
    selection?.addRange(range);
    return false;
  }
}
