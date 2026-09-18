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
