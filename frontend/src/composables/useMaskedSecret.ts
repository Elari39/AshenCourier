/**
 * 「默认打码、换来源就收起」的一次性密钥开关。
 *
 * 这个规则只在一处用（结果卡里的管理密钥），单独抽出来不是因为复用，
 * 而是因为它**必须能被测到**：
 *
 * 结果卡是「被复用」的 —— 落地页与列表页都只是把 `latest` 换成新的 payload，
 * 同一位置的组件实例不会重建。所以 `revealed` 这个组件内部状态会跨链接留着，
 * 第一次点过「显示」之后，下一条短链的一次性管理密钥就默认明文摊在屏幕上，
 * 与「默认打码」的意图正相反，而且**全程没有任何报错**。
 *
 * 为什么不去 e2e 里验：那至少要再创建 2 条短链，而创建接口是 10 次/分钟/IP 的硬配额，
 * `cmd/smoke` 自己要用掉约 7 次 —— 余量本来只有 2。为了验一个纯状态规则去吃掉余量，
 * 换来的是一旦时序稍有偏差 CI 就会偶发 429，不划算。
 * 这条规则只用到 ref/watch，在 vitest 的 node 环境里可以直接跑（项目里 useToast 同理）。
 */
import { ref, watch } from 'vue'

/**
 * @param identity 密钥的「身份」——换了它就说明手里是另一串密钥，要重新收起。
 *   传的是来源标识（比如短码）而不是密钥本身：密钥只在创建那一次出现，
 *   拿它当依赖会连「同一条链接重新渲染」都判成换了来源。
 */
export function useMaskedSecret(identity: () => string | null | undefined) {
  /** 是否处于「已展开」状态。默认 false —— 默认打码才是安全的那一侧。 */
  const revealed = ref(false)

  watch(identity, () => {
    revealed.value = false
  })

  function toggle(): void {
    revealed.value = !revealed.value
  }

  return { revealed, toggle }
}
