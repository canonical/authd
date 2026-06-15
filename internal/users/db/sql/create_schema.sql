CREATE TABLE IF NOT EXISTS users (
    name      TEXT NOT NULL,  -- Uniqueness is enforced by the index below
    uid       INT PRIMARY KEY, -- Uniqueness and not NULL is enforced by PRIMARY KEY
    gid       INT NOT NULL,
    gecos     TEXT DEFAULT "",
    dir       TEXT DEFAULT "",
    shell     TEXT DEFAULT "/bin/bash",
    broker_id   TEXT DEFAULT "",
    locked      BOOLEAN DEFAULT FALSE,
    provider_id TEXT DEFAULT ""  -- Stable provider identifier; uniqueness per broker is enforced by the partial index below
);
CREATE UNIQUE INDEX "idx_user_name" ON users ("name");
CREATE UNIQUE INDEX "idx_user_broker_provider_id" ON users ("broker_id", "provider_id") WHERE broker_id != "" AND provider_id != "";

CREATE TABLE IF NOT EXISTS groups (
    name TEXT NOT NULL,  -- Uniqueness is enforced by the index below
    gid  INT PRIMARY KEY, -- Uniqueness and not NULL is enforced by PRIMARY KEY
    ugid TEXT NOT NULL    -- Uniqueness is enforced by the index below
);
CREATE UNIQUE INDEX "idx_group_name" ON groups ("name");
CREATE UNIQUE INDEX "idx_group_ugid" ON groups ("ugid");

CREATE TABLE IF NOT EXISTS users_to_groups (
    uid INT NOT NULL,
    gid INT NOT NULL,
    PRIMARY KEY (uid, gid),
    FOREIGN KEY (uid) REFERENCES users (uid) ON DELETE CASCADE,
    FOREIGN KEY (gid) REFERENCES groups (gid) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS users_to_local_groups (
    uid        INT NOT NULL,
    group_name TEXT NOT NULL,
    PRIMARY KEY (uid, group_name),
    FOREIGN KEY (uid) REFERENCES users (uid) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS schema_version (
    version INT PRIMARY KEY
);
