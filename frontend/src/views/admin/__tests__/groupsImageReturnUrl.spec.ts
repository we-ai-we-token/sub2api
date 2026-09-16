import { describe, expect, it } from "vitest";

import {
  normalizeGroupImageReturnUrl,
  supportsGroupImageReturnUrl,
} from "../groupsImageReturnUrl";

describe("supportsGroupImageReturnUrl", () => {
  it("只对 openai / gemini 平台开放", () => {
    expect(supportsGroupImageReturnUrl("openai")).toBe(true);
    expect(supportsGroupImageReturnUrl("gemini")).toBe(true);
  });

  it("其余平台一律不支持", () => {
    for (const platform of [
      "anthropic",
      "grok",
      "composite",
      "kimi",
      "zhipu",
      "deepseek",
      "minimax",
      "",
    ]) {
      expect(supportsGroupImageReturnUrl(platform)).toBe(false);
    }
  });
});

describe("normalizeGroupImageReturnUrl", () => {
  it("支持的平台按开关原样透传", () => {
    expect(normalizeGroupImageReturnUrl("openai", true)).toBe(true);
    expect(normalizeGroupImageReturnUrl("gemini", true)).toBe(true);
    expect(normalizeGroupImageReturnUrl("openai", false)).toBe(false);
  });

  it("不支持的平台一律压回 false，避免库里留下永不生效的 true", () => {
    expect(normalizeGroupImageReturnUrl("grok", true)).toBe(false);
    expect(normalizeGroupImageReturnUrl("anthropic", true)).toBe(false);
  });

  it("undefined 视为关闭（表单未初始化时不得意外开启）", () => {
    expect(normalizeGroupImageReturnUrl("openai", undefined)).toBe(false);
  });
});
