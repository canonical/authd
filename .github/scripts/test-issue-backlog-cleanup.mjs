import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const workflow = readFileSync(
  new URL("../workflows/issue-backlog-cleanup.md", import.meta.url),
  "utf8",
);
const AsyncFunction = Object.getPrototypeOf(async function () {}).constructor;

function readScript(indent) {
  const prefix = " ".repeat(indent);
  const match = workflow.match(
    new RegExp(`^${prefix}script: \\|\\n((?:${prefix}  .*\\n|\\n)+)`, "m"),
  );
  assert.ok(match, `Missing script at indentation ${indent}`);
  return match[1].replace(new RegExp(`^${prefix}  `, "gm"), "");
}

const scanWindow = new AsyncFunction("github", "context", "core", readScript(10));
const timeline = new AsyncFunction(
  "inputs",
  "fetch",
  "process",
  `const {issue_number, page} = inputs;\n${readScript(4)}`,
);

async function findWindow(pages, jobs = {}) {
  const outputs = {};
  const checkedRuns = [];
  const github = {
    rest: { actions: {} },
    paginate: Object.assign(
      async (_, { run_id }) => {
        checkedRuns.push(run_id);
        return jobs[run_id] || [];
      },
      {
        iterator: async function* () {
          for (const data of pages) yield { data };
        },
      },
    ),
  };
  github.rest.actions.getWorkflowRun = async () => ({
    data: { created_at: "2026-10-22T05:00:00Z", workflow_id: 1 },
  });
  await scanWindow(
    github,
    {
      repo: { owner: "canonical", repo: "authd" },
      runId: 100,
      payload: { repository: { default_branch: "main" } },
    },
    {
      setOutput: (key, value) => {
        outputs[key] = value;
      },
      info: () => {},
    },
  );
  return { outputs, checkedRuns };
}

test("empty normalized pages use the first-run window", async () => {
  const { outputs } = await findWindow([[]]);
  assert.equal(outputs.since, "2026-10-21T05:00:00.000Z");
  assert.equal(outputs.until, "2026-10-22T05:00:00.000Z");
});

test("pagination skips ineligible runs and agents that did not succeed", async () => {
  const run = (id, event, created_at) => ({ id, event, created_at });
  const { outputs, checkedRuns } = await findWindow(
    [
      [
        run(100, "schedule", "2026-10-20T05:00:00Z"),
        run(99, "schedule", "2026-10-23T05:00:00Z"),
        run(98, "push", "2026-10-21T05:00:00Z"),
        run(96, "schedule", "2026-10-20T05:00:00Z"),
        run(97, "workflow_dispatch", "2026-10-21T05:00:00Z"),
      ],
      [run(95, "schedule", "2026-10-19T05:00:00Z")],
      [run(94, "schedule", "2026-10-18T05:00:00Z")],
    ],
    {
      97: [{ name: "agent", conclusion: "skipped" }],
      96: [{ name: "agent", conclusion: "failure" }],
      95: [{ name: "agent", conclusion: "success" }],
      94: [{ name: "agent", conclusion: "success" }],
    },
  );
  assert.deepEqual(checkedRuns, [97, 96, 95]);
  assert.equal(outputs.since, "2026-10-19T04:00:00.000Z");
});

test("timeline requests stay read-only and repository-scoped across pages", async () => {
  const requests = [];
  const events = [{ event: "marked_as_duplicate" }, { event: "cross-referenced" }];
  const fetch = async (url, options) => {
    requests.push({ url, options });
    return {
      ok: true,
      json: async () => events,
      headers: new Headers(
        requests.length === 1
          ? { link: '<https://api.github.com/repos/canonical/authd/issues/42/timeline?page=2>; rel="next"' }
          : {},
      ),
    };
  };
  const env = { env: { GH_TOKEN: "test-token" } };
  const first = await timeline({ issue_number: 42, page: 1 }, fetch, env);
  assert.deepEqual(first, { events, next_page: 2 });
  const last = await timeline(
    { issue_number: 42, page: first.next_page }, fetch, env,
  );
  assert.deepEqual(last, { events, next_page: null });
  for (const [index, { url, options }] of requests.entries()) {
    assert.equal(
      url,
      `https://api.github.com/repos/canonical/authd/issues/42/timeline?per_page=100&page=${index + 1}`,
    );
    assert.equal(options.method, "GET");
    assert.equal(options.redirect, "error");
    assert.equal(options.headers.Authorization, "Bearer test-token");
  }
});

test("invalid timeline inputs fail before any request", async () => {
  const fetch = () => assert.fail("Unexpected API request");
  const invalidInputs = [
    undefined, null, 0, -1, 1.5, "1/../../other", Number.MAX_SAFE_INTEGER + 1,
  ];
  for (const invalid of invalidInputs) {
    await assert.rejects(
      timeline({ issue_number: invalid, page: 1 }, fetch, {}),
      /positive safe integers/,
    );
    await assert.rejects(
      timeline({ issue_number: 42, page: invalid }, fetch, {}),
      /positive safe integers/,
    );
  }
});

test("timeline API failures are not returned as empty evidence", async () => {
  const inputs = { issue_number: 42, page: 1 };
  const env = { env: {} };
  await assert.rejects(
    timeline(inputs, async () => ({ ok: false, status: 403 }), env),
    /HTTP 403/,
  );
  await assert.rejects(
    timeline(inputs, async () => ({
      ok: true,
      json: async () => ({ message: "not an event list" }),
    }), env),
    /must be an array/,
  );
  await assert.rejects(
    timeline(inputs, async () => { throw new Error("Network timeout"); }, env),
    /Network timeout/,
  );
});
