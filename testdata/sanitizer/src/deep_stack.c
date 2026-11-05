/* Heap overflow seven calls deep, to check top-N frame limits. */
#include <stdio.h>
#include <stdlib.h>

int step7(int *buf, int i) { return buf[i]; }
int step6(int *buf, int i) { return step7(buf, i) + 1; }
int step5(int *buf, int i) { return step6(buf, i) + 1; }
int step4(int *buf, int i) { return step5(buf, i) + 1; }
int step3(int *buf, int i) { return step4(buf, i) + 1; }
int step2(int *buf, int i) { return step3(buf, i) + 1; }
int step1(int *buf, int i) { return step2(buf, i) + 1; }

int main(int argc, char **argv)
{
	(void)argv;
	int *buf = calloc(4, sizeof(int));
	printf("%d\n", step1(buf, 3 + argc));
	free(buf);
	return 0;
}
