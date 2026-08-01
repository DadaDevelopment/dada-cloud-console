import { common, type Messages } from "./common";
import { shell } from "./shell";
import { nav } from "./nav";
import { roles } from "./roles";
import { projects } from "./projects";
import { overview } from "./overview";
import { databases } from "./databases";
import { apps } from "./apps";
import { files } from "./files";
import { operations } from "./operations";
import { git } from "./git";
import { domains } from "./domains";
import { storage } from "./storage";
import { members } from "./members";
import { monitoring } from "./monitoring";
import { models } from "./models";
import { appServers } from "./app-servers";
import { aiStudio } from "./ai-studio";
import { ai } from "./ai";
import { approvals } from "./approvals";
import { audit } from "./audit";
import { adminOverview } from "./admin-overview";
import { adminCosts } from "./admin-costs";
import { aiGatewayUsage } from "./ai-gateway-usage";
import { cloudTasks } from "./cloud-tasks";
import { billing } from "./billing";
import { consumption } from "./consumption";
import { cost } from "./cost";
import { resources } from "./resources";
import { deleteImpact } from "./delete-impact";
import { moveApp } from "./move-app";
import { feedback } from "./feedback";
import { deployHooks } from "./deploy-hooks";
import { previews } from "./previews";
import { agentChat } from "./agent-chat";
import { onboarding } from "./onboarding";
import { passkey } from "./passkey";
import { payments } from "./payments";

/**
 * Flat key→{ru,en} map for the whole console. Each screen owns a namespace
 * fragment ("databases.*", "overview.*"); they are merged here. Keys are dotted
 * and globally unique by namespace, so the spread never collides.
 */
export const messages: Messages = {
  ...common,
  ...shell,
  ...nav,
  ...roles,
  ...projects,
  ...overview,
  ...databases,
  ...apps,
  ...files,
  ...operations,
  ...git,
  ...domains,
  ...storage,
  ...members,
  ...monitoring,
  ...models,
  ...appServers,
  ...aiStudio,
  ...ai,
  ...approvals,
  ...audit,
  ...adminOverview,
  ...adminCosts,
  ...aiGatewayUsage,
  ...cloudTasks,
  ...billing,
  ...consumption,
  ...cost,
  ...resources,
  ...deleteImpact,
  ...moveApp,
  ...feedback,
  ...deployHooks,
  ...previews,
  ...agentChat,
  ...onboarding,
  ...passkey,
  ...payments,
};
