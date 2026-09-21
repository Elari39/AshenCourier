/**
 * UI 层共享的轻量类型。
 *
 * 之所以单独一个文件：`<script setup>` 里不能有具名导出，
 * 分发列表的项类型需要在组件与视图之间共享，就只能放在这里。
 */

/** 一条分布项。`label` 由调用方预先中文化。 */
export interface DistributionItem {
  label: string
  value: number
}

/**
 * 按钮的形态与尺寸。放在这里而不是 Button.vue 内部：
 * `<script setup>` 里不能有具名导出，而复制按钮这类「包一层 Button」的组件
 * 需要沿用同一套取值 —— 各写一份联合类型迟早会漂移（Button 加了新形态，
 * 包装组件却还停在旧的枚举上，编译期看不出来）。
 */
export type ButtonVariant = 'primary' | 'secondary' | 'secondary-dark' | 'text' | 'danger'
export type ButtonSize = 'md' | 'sm'
