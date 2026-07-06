import { common, type Messages } from "./common";
import { shell } from "./shell";
import { nav } from "./nav";
import { roles } from "./roles";
import { projects } from "./projects";
import { overview } from "./overview";
import { databases } from "./databases";
import { apps } from "./apps";
import { operations } from "./operations";
import { git } from "./git";
import { domains } from "./domains";
import { storage } from "./storage";
import { members } from "./members";
import { monitoring } from "./monitoring";
import { models } from "./models";
import { appServers } from "./app-servers";
import { aiStudio } from "./ai-studio";
import { approvals } from "./approvals";
import { cloudTasks } from "./cloud-tasks";
import { billing } from "./billing";
import { consumption } from "./consumption";

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
  ...operations,
  ...git,
  ...domains,
  ...storage,
  ...members,
  ...monitoring,
  ...models,
  ...appServers,
  ...aiStudio,
  ...approvals,
  ...cloudTasks,
  ...billing,
  ...consumption,
};
