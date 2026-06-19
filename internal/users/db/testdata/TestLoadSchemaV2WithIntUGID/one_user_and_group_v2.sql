PRAGMA foreign_keys=OFF;
BEGIN TRANSACTION;
CREATE TABLE IF NOT EXISTS users (
    name      TEXT NOT NULL,
    uid       INT PRIMARY KEY,
    gid       INT NOT NULL,
    gecos     TEXT DEFAULT "",
    dir       TEXT DEFAULT "",
    shell     TEXT DEFAULT "/bin/bash",
    broker_id TEXT DEFAULT "",
    locked    BOOLEAN DEFAULT FALSE
);
CREATE UNIQUE INDEX "idx_user_name" ON users ("name");

CREATE TABLE IF NOT EXISTS GROUPS (
    name TEXT NOT NULL,
    gid  INT PRIMARY KEY,
    ugid INT NOT NULL
);
CREATE UNIQUE INDEX "idx_group_name" ON GROUPS ("name");
CREATE UNIQUE INDEX "idx_group_ugid" ON GROUPS ("ugid");

CREATE TABLE IF NOT EXISTS users_to_groups (
    uid INT NOT NULL,
    gid INT NOT NULL,
    PRIMARY KEY (uid, gid),
    FOREIGN KEY (uid) REFERENCES users (uid) ON DELETE CASCADE,
    FOREIGN KEY (gid) REFERENCES GROUPS (gid) ON DELETE CASCADE
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

-- Seed data using the old schema (ugid as INT)
INSERT INTO users (name, uid, gid, gecos, dir, shell, broker_id, locked)
VALUES ('user1', 1111, 11111, 'User1 gecos', '/home/user1', '/bin/bash', 'broker-id', FALSE);

INSERT INTO GROUPS (name, gid, ugid)
VALUES ('group1', 11111, 12345678);

INSERT INTO users_to_groups (uid, gid)
VALUES (1111, 11111);

-- Old schema v2
INSERT INTO schema_version VALUES (2);
COMMIT;