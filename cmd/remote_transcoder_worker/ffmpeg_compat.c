#include <stdint.h>

#if defined(__GNUC__)
__attribute__((weak))
#endif
int avfilter_compare_sign_bypath(char *signpath1, char *signpath2) {
  (void)signpath1;
  (void)signpath2;
  return -1;
}

#if defined(__GNUC__)
__attribute__((weak))
#endif
int avfilter_compare_sign_bybuff(void *buffer1, int len1, void *buffer2, int len2) {
  (void)buffer1;
  (void)len1;
  (void)buffer2;
  (void)len2;
  return -1;
}
