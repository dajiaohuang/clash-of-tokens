"""Reconcile the exact referenced scope with executable catalog entries."""
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
scope = json.loads((ROOT / "catalog/source_scope.json").read_text(encoding="utf8"))
inventory = json.loads((ROOT / "docs/reference/generic-ai-proxy-master.2026-09-09.json").read_text(encoding="utf8"))
catalog = {x["id"]: x for x in json.loads((ROOT / "catalog/providers.json").read_text(encoding="utf8"))}
assert len(scope) == inventory["count"] == 93
assert len({x["id"] for x in scope}) == 93
assert sorted(x["inventory_number"] for x in scope) == list(range(1, 94))
rows = []
for item in scope:
    reference = next(x for x in inventory["entries"] if x["序号"] == item["inventory_number"])
    entry = catalog.get(item["id"], {})
    rows.append({"id": item["id"], "name": reference["来源家族"], "implementation": entry.get("implementation", "not_implemented"), "live_verified": entry.get("live_verified", False)})
implemented = sum(x["implementation"] != "not_implemented" for x in rows)
live = sum(x["live_verified"] for x in rows)
lines = ["# 严格来源范围与实施状态", "", "原始范围来自参考对话 2026-09-09 最后修订的 93 项 JSON；[原表](reference/generic-ai-proxy-master.2026-09-09.json)保留原始证据主张，仍须按实际参考源码核验。", "", f"范围内已注册适配路径：{implemented}/93；已完成本项目真实上游验证：{live}/93。注册路径仅表示可执行实现存在，不代表其所有功能或认证流程已完成。", "", "官方 API 与本地推理预设作为附加功能保留，不计入此表。未实现项不会通过 init 生成可运行预设。", "", "| 来源 ID | 来源 | 实现 | 账号实测 |", "|---|---|---|---|"]
for row in rows:
    if row["id"] == "phind":
        row["name"] = "Phind（原表名称）；实际参考为 PhindAi / phindai.org，非 phind.com"
    lines.append(f'| `{row["id"]}` | {row["name"]} | {row["implementation"]} | {"已验证" if row["live_verified"] else "未验证"} |')
(ROOT / "docs/SOURCE_STATUS.md").write_text("\n".join(lines) + "\n", encoding="utf8")
print(json.dumps({"scope": len(rows), "registered_implementations": implemented, "live_verified": live}))
