import { defineConfig, globalIgnores } from "eslint/config";
import nextVitals from "eslint-config-next/core-web-vitals";
import nextTs from "eslint-config-next/typescript";

const eslintConfig = defineConfig([
  ...nextVitals,
  ...nextTs,
  globalIgnores([
    ".next/**",
    "out/**",
    "build/**",
    "next-env.d.ts",
  ]),
  {
    files: ["app/(console)/admin/audit/page.tsx"],
    rules: { "react-hooks/set-state-in-effect": "off" },
  },
  {
    files: [
      "app/(console)/projects/\\[projectId\\]/apps/page.tsx",
      "components/deploy/deploy-hooks-card.tsx",
      "app/(console)/admin/page.tsx",
      "app/(console)/admin/costs/page.tsx",
      "app/(console)/admin/ai-gateway/page.tsx",
      "app/(console)/admin/feedback/page.tsx",
      "app/(console)/admin/db-shards/page.tsx",
      "app/(console)/projects/\\[projectId\\]/databases/\\[name\\]/tables/\\[table\\]/page.tsx",
      "components/databases/db-activity.tsx",
    ],
    rules: { "react-hooks/set-state-in-effect": "off" },
  },
  {
    files: ["components/console/template-deploy-cards.tsx"],
    rules: { "react-hooks/purity": "off" },
  },
]);

export default eslintConfig;
