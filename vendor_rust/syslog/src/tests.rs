use io::Read;
use std::{collections::BTreeMap, sync::Barrier};

#[test]
fn test_unix_socket() {
    use super::*;
    // create a unix socket listener on a random path
    let path = std::env::temp_dir().join(format!("syslog-test-{}", std::process::id()));
    let formatter = Formatter3164 {
        facility: Facility::LOG_USER,
        hostname: None,
        process: "myprogram".into(),
        pid: 0,
    };

    let err = unix_custom(formatter.clone(), &path)
        .map(|_| ())
        .unwrap_err();
    println!("expected initialization error: {err:?}");

    let listener = std::os::unix::net::UnixListener::bind(&path).unwrap();

    let str = Arc::new(Mutex::new(String::new()));
    let s = str.clone();

    let barrier = Arc::new(Barrier::new(2));
    let b = barrier.clone();
    std::thread::spawn(move || {
        let mut stream = listener.accept().unwrap().0;
        let mut locked = s.lock().unwrap();

        b.wait();
        while let Ok(sz) = stream.read_to_string(&mut locked) {
            println!("string is now(sz={sz}): {locked}");
            if sz == 0 {
                break;
            }
        }
    });

    let mut writer: Logger<LoggerBackend, Formatter3164> = unix_custom(formatter, &path).unwrap();
    barrier.wait();
    writer.emerg("a1").unwrap();
    writer.alert("a2").unwrap();
    writer.crit("a3").unwrap();
    writer.err("a4").unwrap();
    writer.warning("a5").unwrap();
    writer.notice("a6").unwrap();
    writer.info("a7").unwrap();
    writer.debug("a8").unwrap();
    drop(writer);

    let s = str.lock().unwrap();
    println!("messages:\n{}", *s);

    assert!(s.contains("a1"));
    assert!(s.contains("a2"));
    assert!(s.contains("a3"));
    assert!(s.contains("a4"));
    assert!(s.contains("a5"));
    assert!(s.contains("a6"));
    assert!(s.contains("a7"));
    assert!(s.contains("a8"));
    assert!(s.contains("myprogram"));
}

#[test]
fn test_tcp() {
    use super::*;
    let formatter = Formatter5424 {
        facility: Facility::LOG_USER,
        hostname: None,
        process: "myprogram".into(),
        pid: 0,
    };

    let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    let local_address = listener.local_addr().unwrap();

    let str = Arc::new(Mutex::new(String::new()));
    let s = str.clone();

    let barrier = Arc::new(Barrier::new(2));
    let b = barrier.clone();
    std::thread::spawn(move || {
        let mut stream = listener.accept().unwrap().0;
        let mut locked = s.lock().unwrap();

        b.wait();
        while let Ok(sz) = stream.read_to_string(&mut locked) {
            println!("string is now(sz={sz}): {locked}");

            if sz == 0 {
                break;
            }
        }
    });

    let mut writer: Logger<LoggerBackend, Formatter5424> = tcp(formatter, local_address).unwrap();
    barrier.wait();

    writer.emerg((1, BTreeMap::new(), "a1")).unwrap();
    let mut data = BTreeMap::new();
    let mut inner = BTreeMap::new();
    inner.insert("key".to_string(), "value".to_string());
    data.insert("param".to_string(), inner);

    writer.alert((1, data.clone(), "a2")).unwrap();
    writer.crit((1, data.clone(), "a3")).unwrap();
    writer.err((1, data.clone(), "a4")).unwrap();
    writer.warning((1, data.clone(), "a5")).unwrap();
    writer.notice((1, data.clone(), "a6")).unwrap();
    writer.info((1, data.clone(), "a7")).unwrap();
    writer.debug((1, data.clone(), "a8")).unwrap();

    drop(writer);

    let s = str.lock().unwrap();
    println!("messages:\n{}", *s);

    assert!(s.contains("a1"));
    assert!(s.contains("a2"));
    assert!(s.contains("a3"));
    assert!(s.contains("a4"));
    assert!(s.contains("a5"));
    assert!(s.contains("a6"));
    assert!(s.contains("a7"));
    assert!(s.contains("a8"));
    assert!(s.contains("myprogram"));
    assert!(s.contains("[param key=\"value\"]"));
}

#[test]
fn test_udp() {
    use super::*;
    let formatter = Formatter5424 {
        facility: Facility::LOG_USER,
        hostname: None,
        process: "myprogram".into(),
        pid: 0,
    };

    let listener = std::net::UdpSocket::bind("127.0.0.1:0").unwrap();
    let local_address = listener.local_addr().unwrap();

    let str = Arc::new(Mutex::new(String::new()));
    let s = str.clone();

    let barrier = Arc::new(Barrier::new(2));
    let b = barrier.clone();
    std::thread::spawn(move || {
        let mut locked: std::sync::MutexGuard<String> = s.lock().unwrap();

        b.wait();
        let mut buf = [0u8; 1024];

        let mut counter = 0;
        while let Ok((sz, _)) = listener.recv_from(&mut buf) {
            locked.push_str(&std::str::from_utf8(&buf[..sz]).unwrap());
            println!("string is now(sz={sz}): {locked}");

            counter += 1;
            if counter == 8 {
                break;
            }
        }
    });

    let mut writer: Logger<LoggerBackend, Formatter5424> =
        udp(formatter, "127.0.0.1:0", local_address).unwrap();
    barrier.wait();

    writer.emerg((1, BTreeMap::new(), "a1")).unwrap();
    let mut data = BTreeMap::new();
    let mut inner = BTreeMap::new();
    inner.insert("key".to_string(), "value".to_string());
    data.insert("param".to_string(), inner);

    writer.alert((1, data.clone(), "a2")).unwrap();
    writer.crit((1, data.clone(), "a3")).unwrap();
    writer.err((1, data.clone(), "a4")).unwrap();
    writer.warning((1, data.clone(), "a5")).unwrap();
    writer.notice((1, data.clone(), "a6")).unwrap();
    writer.info((1, data.clone(), "a7")).unwrap();
    writer.debug((1, data.clone(), "a8")).unwrap();

    drop(writer);

    let s = str.lock().unwrap();
    println!("messages:\n{}", *s);

    assert!(s.contains("a1"));
    assert!(s.contains("a2"));
    assert!(s.contains("a3"));
    assert!(s.contains("a4"));
    assert!(s.contains("a5"));
    assert!(s.contains("a6"));
    assert!(s.contains("a7"));
    assert!(s.contains("a8"));
    assert!(s.contains("myprogram"));
    assert!(s.contains("[param key=\"value\"]"));
}
