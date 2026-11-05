/* Frees the same buffer twice. */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

char *copy_name(const char *name)
{
	char *p = malloc(strlen(name) + 1);
	strcpy(p, name);
	return p;
}

void release(char *p)
{
	free(p);
}

int main(int argc, char **argv)
{
	char *p = copy_name(argc > 1 ? argv[1] : "data");
	puts(p);
	release(p);
	release(p);
	return 0;
}
