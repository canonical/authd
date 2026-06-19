#include "nss.h"

#ifdef INTEGRATION_TESTS
// db_override configures the local nss lookup to use the authd module.
void db_override() {
    __nss_configure_lookup("passwd", "files authd");
    __nss_configure_lookup("group", "files authd");
    __nss_configure_lookup("shadow", "files authd");
}
#endif
