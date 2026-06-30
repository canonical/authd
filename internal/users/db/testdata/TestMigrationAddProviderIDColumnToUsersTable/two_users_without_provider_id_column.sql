PRAGMA foreign_keys=OFF;
BEGIN TRANSACTION;
CREATE TABLE users (
    name      TEXT NOT NULL,  -- Uniqueness is enforced by the index below
    uid       INT PRIMARY KEY, -- Uniqueness and not NULL is enforced by PRIMARY KEY
    gid       INT NOT NULL,
    gecos     TEXT DEFAULT "",
    dir       TEXT DEFAULT "",
    shell     TEXT DEFAULT "/bin/bash",
    broker_id TEXT DEFAULT "",
    locked    BOOLEAN DEFAULT FALSE
);
INSERT INTO users VALUES('user1',1111,11111,'User1 gecos','/home/user1','/bin/bash','broker-id',0);
INSERT INTO users VALUES('user2',2222,22222,'User2 gecos','/home/user2','/bin/bash','broker-id',0);
CREATE TABLE GROUPS (
    name TEXT NOT NULL,  -- Uniqueness is enforced by the index below
    gid  INT PRIMARY KEY, -- Uniqueness and not NULL is enforced by PRIMARY KEY
    ugid INT NOT NULL    -- Uniqueness is enforced by the index below
);
INSERT INTO "GROUPS" VALUES('group1',11111,12345678);
INSERT INTO "GROUPS" VALUES('group2',22222,87654321);
CREATE TABLE users_to_groups (
    uid INT NOT NULL,
    gid INT NOT NULL,
    PRIMARY KEY (uid, gid),
    FOREIGN KEY (uid) REFERENCES users (uid) ON DELETE CASCADE,
    FOREIGN KEY (gid) REFERENCES GROUPS (gid) ON DELETE CASCADE
);
INSERT INTO users_to_groups VALUES(1111,11111);
INSERT INTO users_to_groups VALUES(2222,22222);
CREATE TABLE users_to_local_groups (
    uid        INT NOT NULL,
    group_name TEXT NOT NULL,
    PRIMARY KEY (uid, group_name),
    FOREIGN KEY (uid) REFERENCES users (uid) ON DELETE CASCADE
);
CREATE TABLE schema_version (
    version INT PRIMARY KEY
);
INSERT INTO schema_version VALUES(2);
CREATE UNIQUE INDEX "idx_user_name" ON users ("name");
CREATE UNIQUE INDEX "idx_group_name" ON GROUPS ("name");
CREATE UNIQUE INDEX "idx_group_ugid" ON GROUPS ("ugid");
COMMIT;
