# libnss-rs
Rust bindings for creating libnss modules.

Currently supports the following databases:
- passwd
- shadow
- group
- hosts

## Getting started
- Create a new library
  ```bash
  cargo new nss_example --lib
  ```
- Change library type to ```cdylib``` in your ```Cargo.toml```
  ```yaml
  [lib]
  name = "nss_example"
  crate-type = [ "cdylib" ]
  ```
  *** NOTE *** The name of the crate itself is not important, however the library itself must follow the ```nss_xxx``` pattern.

- Add ```libnss``` to your ```Cargo.toml```
  ```yaml
  [dependencies]
  libc = "0.2.0"
  libnss = "0.8.0"
  ```

- Implement a ```passwd``` database
  ```rust
  use libnss::passwd::{PasswdHooks, Passwd};
  use libnss::libnss_passwd_hooks;
  
  struct ExamplePasswd;
  libnss_passwd_hooks!(example, ExamplePasswd);
  ```
  It is important that the first param of ```libnss_passwd_hooks``` is the name of your final library ```libnss_example.so.2```
  ````rust
  impl PasswdHooks for HardcodedPasswd {
      fn get_all_entries() -> Vec<Passwd> {
          vec![
              Passwd {
                  name: "test".to_string(),
                  passwd: "x".to_string(),
                  uid: 1005,
                  gid: 1005,
                  gecos: "Test Account".to_string(),
                  dir: "/home/test".to_string(),
                  shell: "/bin/bash".to_string(),
              }
          ]
      }
  
      fn get_entry_by_uid(uid: libc::uid_t) -> Option<Passwd> {
          if uid == 1005 {
              return Some(Passwd {
                  name: "test".to_string(),
                  passwd: "x".to_string(),
                  uid: 1005,
                  gid: 1005,
                  gecos: "Test Account".to_string(),
                  dir: "/home/test".to_string(),
                  shell: "/bin/bash".to_string(),
              });
          }
  
          None
      }
  
      fn get_entry_by_name(name: String) -> Option<Passwd> {
          if name == "test" {
              return Some(Passwd {
                  name: "test".to_string(),
                  passwd: "x".to_string(),
                  uid: 1005,
                  gid: 1005,
                  gecos: "Test Account".to_string(),
                  dir: "/home/test".to_string(),
                  shell: "/bin/bash".to_string(),
              });
          }
  
          None
      }
  }
  ````
- Build
  ```
  cargo build --release
  ```
- Install the library
  ```bash
  cd target/release
  cp libnss_example.so libnss_example.so.2
  sudo install -m 0644 libnss_example.so.2 /lib
  sudo /sbin/ldconfig -n /lib /usr/lib
  ```
- Enable your nss module in ```/etc/nsswitch.conf```
  eg:
  ```
  passwd:         example files systemd
  ```
  The name in here must follow the final library name ```libnss_example.so.2```
- Look at the examples for more information
