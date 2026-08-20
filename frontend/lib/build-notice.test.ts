import test from "node:test";
import assert from "node:assert/strict";
import {
  classifyBuildNotice,
  buildNoticeFailureKeys,
  buildNoticeNotifyFailureKeys,
  buildNoticeUxTarget,
} from "./build-notice.ts";
import { buildWatcher } from "./i18n/console/messages/build-watcher.ts";

test("a platform_error build is our failure, not the user's", () => {
  assert.equal(classifyBuildNotice("platform_error"), "platform");
});

test("an app deleted mid-build is nobody's failure", () => {
  assert.equal(classifyBuildNotice("app_deleted"), "appDeleted");
});

test("real build failures stay build failures", () => {
  for (const reason of ["no_dockerfile", "dockerfile_build_failed", "git_auth_failed", "build_failed"]) {
    assert.equal(classifyBuildNotice(reason), "build", reason);
  }
});

test("an unknown or absent fail_reason never claims the platform broke", () => {
  assert.equal(classifyBuildNotice(undefined), "build");
  assert.equal(classifyBuildNotice(null), "build");
  assert.equal(classifyBuildNotice(""), "build");
  assert.equal(classifyBuildNotice("something_we_have_not_seen"), "build");
});

test("every kind resolves to a message key that actually exists", () => {
  for (const kind of ["platform", "appDeleted", "build"] as const) {
    for (const keys of [buildNoticeFailureKeys(kind), buildNoticeNotifyFailureKeys(kind)]) {
      assert.ok(buildWatcher[keys.title], `missing message ${keys.title}`);
      assert.ok(buildWatcher[keys.body], `missing message ${keys.body}`);
    }
  }
});

test("the platform wording does not blame the user's code, in either language", () => {
  const ru = buildWatcher["buildWatcher.failure.platform.body"].ru;
  const en = buildWatcher["buildWatcher.failure.platform.body"].en;
  assert.match(ru, /не из-за вашего кода/);
  assert.match(en, /not in your code/);
  assert.notEqual(ru, buildWatcher["buildWatcher.failure.build.body"].ru);
  assert.notEqual(en, buildWatcher["buildWatcher.failure.build.body"].en);
});

test("telemetry separates an outage notice from a build-failure notice", () => {
  assert.equal(buildNoticeUxTarget("failed", "platform"), "build_notice:failure:platform");
  assert.equal(buildNoticeUxTarget("failed", "build"), "build_notice:failure:build");
  assert.equal(buildNoticeUxTarget("failed", "appDeleted"), "build_notice:failure:appDeleted");
  assert.equal(buildNoticeUxTarget("success", "build"), "build_notice:success");
});
