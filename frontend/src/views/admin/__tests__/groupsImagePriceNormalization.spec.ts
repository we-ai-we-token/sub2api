import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// GroupsView.vue 的价格归一化是两段内联清单：创建走 emptyToNull（"" → null），
// 更新走 emptyPriceToClear（"" | null → -1）。清单是手写的，新增一档价格时很容易漏，
// 而漏掉的后果是清空该输入框后 v-model.number 提交 ""，后端 *float64 绑定直接 400。
// 二开的质量分级价（low/medium/high）就是这么漏掉的，所以这里直接读源码做断言：
// 只要 imageSizePricingTiers / imageQualityPricingTiers 里出现新 key，两段清单都必须跟上。
const source = readFileSync(
  resolve(process.cwd(), "src/views/admin/GroupsView.vue"),
  "utf-8",
);

const tierKeys = (declaration: string): string[] => {
  const block = source.match(
    new RegExp(`const ${declaration} = \\[([\\s\\S]*?)\\] as const;`),
  );
  expect(block, `${declaration} 声明未找到，测试锚点已失效`).not.toBeNull();
  const keys = [...block![1].matchAll(/key:\s*"([^"]+)"/g)].map((m) => m[1]);
  expect(keys.length).toBeGreaterThan(0);
  return keys;
};

const normalizedBy = (helper: string): Set<string> =>
  new Set(
    [
      ...source.matchAll(
        new RegExp(`\\.([a-z0-9_]+)\\s*=\\s*${helper}\\(`, "g"),
      ),
    ].map((m) => m[1]),
  );

describe("GroupsView 图片价格归一化清单", () => {
  const priceKeys = [
    ...tierKeys("imageSizePricingTiers"),
    ...tierKeys("imageQualityPricingTiers"),
  ];

  it.each(priceKeys)("创建分组时 %s 会被 emptyToNull 归一化", (key) => {
    expect(normalizedBy("emptyToNull")).toContain(key);
  });

  it.each(priceKeys)("更新分组时 %s 会被 emptyPriceToClear 归一化", (key) => {
    expect(normalizedBy("emptyPriceToClear")).toContain(key);
  });
});
