PRAGMA foreign_keys=OFF;
BEGIN TRANSACTION;
CREATE TABLE users (
    name      TEXT NOT NULL,  -- Uniqueness is enforced by the index below
    uid       INT PRIMARY KEY, -- Uniqueness and not NULL is enforced by PRIMARY KEY
    gid       INT NOT NULL,
    gecos     TEXT DEFAULT "",
    dir       TEXT DEFAULT "",
    shell     TEXT DEFAULT "/bin/bash",
    broker_id TEXT DEFAULT ""
);
INSERT INTO users VALUES('TESTUSER',1111,11111,'testuser gecos','/home/testuser','/bin/bash','broker-id');
CREATE TABLE GROUPS (
    name TEXT NOT NULL,  -- Uniqueness is enforced by the index below
    gid  INT PRIMARY KEY, -- Uniqueness and not NULL is enforced by PRIMARY KEY
    ugid INT NOT NULL    -- Uniqueness is enforced by the index below
);
INSERT INTO "GROUPS" VALUES('testgroup',11111,12345678);
INSERT INTO "GROUPS" VALUES('TESTGROUP',22222,56781234);
CREATE TABLE users_to_groups (
    uid INT NOT NULL,
    gid INT NOT NULL,
    PRIMARY KEY (uid, gid),
    FOREIGN KEY (uid) REFERENCES users (uid) ON DELETE CASCADE,
    FOREIGN KEY (gid) REFERENCES GROUPS (gid) ON DELETE CASCADE
);
INSERT INTO users_to_groups VALUES(1111,11111);
INSERT INTO users_to_groups VALUES(1111,22222);
CREATE TABLE users_to_local_groups (
    uid        INT NOT NULL,
    group_name TEXT NOT NULL,
    PRIMARY KEY (uid, group_name),
    FOREIGN KEY (uid) REFERENCES users (uid) ON DELETE CASCADE
);
CREATE TABLE schema_version (
    version INT PRIMARY KEY
);
INSERT INTO schema_version VALUES(0);
CREATE UNIQUE INDEX "idx_user_name" ON users ("name");
CREATE UNIQUE INDEX "idx_group_name" ON GROUPS ("name");
CREATE UNIQUE INDEX "idx_group_ugid" ON GROUPS ("ugid");
COMMIT;
