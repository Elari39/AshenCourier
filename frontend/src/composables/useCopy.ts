/**
 * 复制到剪贴板。
 *
 * 优先用异步 Clipboard API；在非 HTTPS 环境（例如局域网 IP 直连）下
 * navigator.clipboard 不可用，退回到隐藏 textarea + execCommand 的老办法。
 */

import { onUnmounted, ref } from 'vue'

/** 复制能力的返回结构。 */
export function useCopy() {
  /** 是否处于「刚刚复制成功」的高亮态，用于按钮文案切换。 */
  const copied = ref(false)
  let resetTimer: number | undefined

  async function copy(text: string): Promise<boolean> {
    const ok = (await writeClipboard(text)) !== false
    if (ok) {
      copied.value = true
      window.clearTimeout(resetTimer)
      resetTimer = window.setTimeout(() => {
        copied.value = false
      }, 1800)
    }
    return ok
  }

  // 卸载时清掉还没触发的复位定时器。留着的话它会在 1.8 秒后于已销毁的组件上写 ref
  // （无害，但属于「组件走了定时器还在」这类不该留的悬挂回调）。
  onUnmounted(() => window.clearTimeout(resetTimer))

  return { copied, copy }
}

/** 实际写入剪贴板。成功返回 true，失败返回 false。 */
async function writeClipboard(text: string): Promise<boolean> {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text)
      return true
    } catch {
      // 权限被拒或非安全上下文：继续走回退方案
    }
  }
  return fallbackWrite(text)
}

/** 老办法：临时塞一个 textarea、选中、execCommand('copy')。 */
function fallbackWrite(text: string): boolean {
  const area = document.createElement('textarea')
  area.value = text
  area.setAttribute('readonly', '')
  // 放到视口外，避免页面跳动
  area.style.position = 'fixed'
  area.style.top = '-9999px'
  document.body.appendChild(area)

  try {
    area.select()
    return document.execCommand('copy')
  } catch {
    return false
  } finally {
    document.body.removeChild(area)
  }
}
