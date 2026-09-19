/* Loads the built plugin exactly as the CLIProxyAPI host does.
 *
 * Calls cliproxy_plugin_init through dlsym, then dispatches plugin.register
 * and prints the returned payload. Use this to verify the C ABI surface
 * without a running host.
 *
 * Build and run:
 *   gcc scripts/loadtest.c -o /tmp/loadtest -ldl && /tmp/loadtest
 */
#include <stdio.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <dlfcn.h>

typedef struct { void* ptr; size_t len; } cliproxy_buffer;
typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);
typedef struct { uint32_t abi_version; void* host_ctx; cliproxy_host_call_fn call; cliproxy_host_free_fn free_buffer; } cliproxy_host_api;
typedef int (*cliproxy_plugin_call_fn)(const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);
typedef struct { uint32_t abi_version; cliproxy_plugin_call_fn call; cliproxy_plugin_free_fn free_buffer; cliproxy_plugin_shutdown_fn shutdown; } cliproxy_plugin_api;
typedef int (*init_fn)(const cliproxy_host_api*, cliproxy_plugin_api*);

int main(void) {
  void* h = dlopen("./dist/commandcode-bridge.so", RTLD_NOW);
  if (!h) { printf("DLOPEN FAILED: %s\n", dlerror()); return 1; }
  init_fn init = (init_fn)dlsym(h, "cliproxy_plugin_init");
  if (!init) { printf("MISSING cliproxy_plugin_init\n"); return 1; }

  cliproxy_plugin_api p;
  memset(&p, 0, sizeof(p));
  int rc = init(NULL, &p);
  printf("init rc=%d abi=%u call=%p shutdown=%p\n", rc, p.abi_version, (void*)p.call, (void*)p.shutdown);
  if (rc != 0 || p.abi_version != 1 || p.call == NULL) { printf("ABI SURFACE INVALID\n"); return 1; }

  cliproxy_buffer out;
  out.ptr = NULL;
  out.len = 0;
  int c = p.call("plugin.register", NULL, 0, &out);
  printf("plugin.register rc=%d len=%zu\n", c, out.len);
  if (out.ptr && out.len > 0) { printf("PAYLOAD: %.*s\n", (int)out.len, (char*)out.ptr); }
  return 0;
}
