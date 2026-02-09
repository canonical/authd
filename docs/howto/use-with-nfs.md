---
myst:
  html_meta:
    "description lang=en": "Use authd with an NFS server."
---

# Using authd with NFS

The user identifiers (UIDs) and group identifiers (GIDs) assigned by authd are
unique to each machine. This means that when using authd with NFS, the UIDs and
GIDs of users and groups on the NFS server will not match those on the client
machines, which leads to permission issues.

To avoid these issues, you can use NFS with ID mapping and Kerberos. This
ensures that the UIDs and GIDs are mapped correctly across all machines.

## Setting up NFS with IDMAP and Kerberos

This guide will walk you through setting up an NFS server with ID mapping and
Kerberos authentication. After following the steps outlined below, the user
`alice` will be able to access a shared directory on the server from a client
machine.

---

### Steps for the server

#### Step 1: Install required packages

1. **Install packages:**
   On the NFS server, run:
   ```shell
   sudo apt install -y nfs-kernel-server nfs-common rpcbind krb5-user krb5-admin-server krb5-kdc
   ```

2. **Handle Kerberos configuration prompts:**
   During the installation of `krb5-user`, you will be prompted to provide
   configuration details for Kerberos. Here's what to enter:

   - **Default Kerberos version 5 realm:**
     Enter the Kerberos realm name, which is the uppercase version of your
     domain. For example:
     ```
     EXAMPLE.COM
     ```

   - **Kerberos servers for your realm:**
     Enter the hostname of the Key Distribution Center (KDC). Assuming the KDC
     is on the same host as the NFS server:
     ```
     server.example.com
     ```

   - **Administrative server for your Kerberos realm:**
     Enter the hostname of the Kerberos admin server, which is also the same as
     the NFS server in this case:
     ```
     server.example.com
     ```

---

#### Step 2: Configure Kerberos

1. **Create the Realm:**
   ```shell
   sudo krb5_newrealm
   ```
   Follow the prompts to set up the Kerberos realm.

2. **Add principals:**
   In Kerberos, a principal is a unique identity that is used for authentication.

   - Add a principal for the NFS server:
     This principal is used by the NFS client to authenticate when mounting an
     NFS directory.
     ```shell
     sudo kadmin.local addprinc -randkey nfs/server.example.com
     ```

   - Add a principal for the user `alice`:
     This principal is used for authentication when the user accesses the
     mounted NFS directory.
     ```shell
     sudo kadmin.local addprinc alice
     ```
     When prompted, set a password for the user `alice`.

3. **Generate Keytabs:**

   A *keytab* is a file that contains Kerberos principals and their associated
   secret keys. It allows services (such as NFS) to authenticate without needing
   to input a password each time.

   - Export the keytab for the NFS server and the user `alice`:
     ```shell
     sudo kadmin.local ktadd -k /etc/krb5.keytab nfs/server.example.com
     ```

---

#### Step 3: Configure the NFS server

1. **Create and configure the shared directory:**

   You’ll need to create the directory to share via NFS and configure the shared
   directory in the `/etc/exports` file.

   - **Create a directory owned by `alice`:**
     ```shell
     sudo mkdir -p /srv/nfs/shared/alice
     sudo chown alice:alice /srv/nfs/shared/alice
     ```

   - **Configure exports:**
     Edit the `/etc/exports` file to define the shared directory:
     ```shell
     sudo editor /etc/exports
     ```
     Add this line:
     ```
     /srv/nfs/shared *(rw,sync,no_subtree_check,sec=krb5)
     ```

2. **Configure IDMAP:**
   Edit the IDMAP configuration:
   ```shell
   sudo editor /etc/idmapd.conf
   ```
   Ensure the following is set:
   ```ini
   [General]
   Domain = example.com
   ```

3. **Restart services:**
   ```shell
   sudo systemctl restart nfs-kernel-server rpcbind rpc-svcgssd
   ```

4. **Verify running services:**
   Check the status of the relevant services:
   ```shell
   sudo systemctl status nfs-kernel-server rpcbind rpc-svcgssd
   ```

---

### Steps for the client

#### Step 1: Install required packages

1. **Install packages:**
   On the NFS client, run:
   ```shell
   sudo apt install -y nfs-common krb5-user rpcbind
   ```

2. **Handle Kerberos configuration prompts:**
   During the installation of `krb5-user`, you will be prompted to provide
   configuration details for Kerberos again. Enter the same details as before:

   - **Default Kerberos version 5 realm:**
     ```
     EXAMPLE.COM
     ```

   - **Kerberos servers for your realm:**
     ```
     server.example.com
     ```

   - **Administrative server for your Kerberos realm:**
     ```
     server.example.com
     ```

---

#### Step 2: Copy the Kerberos keytab file

1. **Copy keytab file:**
   Securely copy the keytab from the server to the client and set the correct
   permissions:
   ```shell
   scp root@server.example.com:/etc/krb5.keytab /tmp/krb5.keytab && \
   sudo mv /tmp/krb5.keytab /etc/krb5.keytab && \
   sudo chown root:root /etc/krb5.keytab && \
   sudo chmod 600 /etc/krb5.keytab
   ```

---

#### Step 3: Configure NFS client

1. **Configure IDMAP:**
   Edit the IDMAP configuration:
   ```shell
   sudo editor /etc/idmapd.conf
   ```
   Ensure the following is set:
   ```ini
   [General]
   Domain = example.com
   ```

2. **Restart services:**
   ```shell
   sudo systemctl restart nfs-client.target rpc-gssd.service rpcbind.service
   ```

3. **Verify running services:**
   Check the status of the relevant services:
   ```shell
   sudo systemctl status nfs-client.target rpc-gssd.service auth-rpcgss-module.service rpcbind.service
   ```

---

#### Step 4: Mount the NFS share

Mount the shared directory with Kerberos security:
```shell
sudo -u alice mkdir /home/alice/nfs
sudo mount -t nfs4 -o sec=krb5 server.example.com:/srv/nfs/shared/alice /home/alice/nfs
```

---

#### Step 5: Obtain Kerberos ticket

Log in as the user `alice` and authenticate:
```shell
kinit alice
```

Verify the ticket:
```shell
klist
```

---

### Step 6: Test and debug

1. **Test access to the share:**
   As the user `alice`, try accessing the share:
   ```shell
   ls -la /home/alice/nfs
   ```

   Create a test file to verify write access:
   ```shell
   touch /home/alice/nfs/test
   ```

2. **Check logs if issues arise:**

   - On the server:
     ```shell
     sudo journalctl -u nfs-kernel-server -u rpcbind -u rpc-svcgssd
     ```

   - On the client:
     ```shell
     sudo journalctl -u rpcbind -u rpc-gssd
     ```

---

### Cleanup

If you no longer need the NFS share or want to clean up the configuration,
follow these steps:

#### On the server

1. **Purge installed packages:**
   ```shell
   sudo apt purge "krb*" "nfs-*"
   ```

2. **Remove Kerberos configuration and data:**
   ```shell
   sudo sh -c "rm -rf /etc/krb5* /var/lib/krb5kdc/* /tmp/krb5*"
   ```

3. **Remove the shared directory:**
   ```shell
   sudo rm -rf /srv/nfs/shared
   sudo rmdir /srv/nfs
   ```
#### On the client

1. **Unmount the shared directory and delete the mountpoint:**
   ```shell
   sudo umount /home/alice/nfs
   sudo rmdir /home/alice/nfs
   ```

2. **Purge installed packages:**
   ```shell
   sudo apt purge nfs-common krb5-* rpcbind
   ```

3. **Remove Kerberos data:**
   ```shell
   sudo rm -f /etc/krb5.keytab /tmp/krb5*
   ```

---

## Additional resources

For a complete guide to setting up NFS on your client and server, see [Network File System (NFS)](https://documentation.ubuntu.com/server/how-to/networking/install-nfs/) in the Ubuntu Server documentation.
